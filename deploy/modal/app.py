# Modal (modal.com), image and video fleets in one App (docs/DEPLOY.md "Modal").
#
#   pip install modal==1.6.2 && modal setup                  # once, on your machine
#   modal secret create ghcr-pull REGISTRY_USERNAME=<github-user> REGISTRY_PASSWORD=<token-with-read:packages>
#   modal secret create xtandard-image-signing XI_SIGNING_KEYS=<hex> XI_SIGNING_SALTS=<hex>
#   modal secret create xtandard-image-stream XI_VIDEO_TRANSFORM_STREAM_KEYS=<hex>
#   modal deploy deploy/modal/app.py
#
# `modal deploy` prints two URLs, https://<workspace>--xtandard-image.modal.run (stills) and
# https://<workspace>--xtandard-video.modal.run (video). Route /video/, /q/video and /_stream/ to
# the second one at your CDN, everything else to the first (docs/DEPLOY.md "Image and video
# fleets"). With only stills, delete the VideoFleet class and its secret.
#
# How it runs: Modal ignores the image's USER and runs its own Python entrypoint in the
# container, so the image gets a standalone Python (add_python), its ENTRYPOINT is cleared, and
# each container starts `xtandard-image serve` as a subprocess (as UID 65532, the image's user)
# and proxies HTTP to port 8080 (@modal.web_server). Every HTTP request is one Modal "input":
# @modal.concurrent sets how many a container takes, and Modal's autoscaler adds containers
# between min_containers and max_containers to keep each near target_inputs.
#
# Limits that matter here (Modal docs, 2026-10): 150 s per HTTP request, request bodies up to
# 4 GiB, 200 requests per second per workspace on new accounts (429 above it; ask Modal to raise
# it), a 30 s grace period for @modal.exit when a container stops.
from __future__ import annotations

import os
import signal
import subprocess

import modal

VERSION = "0.1.1"
SERVER = "/usr/local/bin/xtandard-image"
PORT = 8080
UID = GID = 65532  # the image's xtandard user; Modal itself runs containers as root

# Private GHCR package: a Modal Secret with REGISTRY_USERNAME and REGISTRY_PASSWORD (a GitHub token
# with read:packages). Modal needs a linux/amd64 image; ours is multi-arch (amd64 and arm64).
registry = modal.Secret.from_name("ghcr-pull", required_keys=["REGISTRY_USERNAME", "REGISTRY_PASSWORD"])
signing = modal.Secret.from_name("xtandard-image-signing", required_keys=["XI_SIGNING_KEYS", "XI_SIGNING_SALTS"])
stream = modal.Secret.from_name("xtandard-image-stream", required_keys=["XI_VIDEO_TRANSFORM_STREAM_KEYS"])


def server_image(flavor: str) -> modal.Image:
    # The runtime image is Ubuntu 24.04 (glibc) without Python: add_python copies a standalone
    # CPython into /usr/local for Modal's own runtime. The image's ENTRYPOINT is the server
    # binary, and Modal runs its Python entrypoint through the image's ENTRYPOINT (which must
    # exec its arguments), so clear it.
    return modal.Image.from_registry(
        f"ghcr.io/xantiagoma/xtandard-image:{VERSION}-{flavor}", secret=registry, add_python="3.13"
    ).entrypoint([])


# Server settings (docs/CONFIG.md). Literal strings only: deploy/check/validate.py reads these
# dicts and runs `xtandard-image config validate` on each.
IMAGE_ENV = {
    "XI_ROLE": "image",
    "XI_SOURCES": "web",
    "XI_SOURCES_WEB_TYPE": "http",
    "XI_SOURCES_WEB_ALLOW": "images.example.com",
    # No shared disk on Modal: an in-memory result cache per container, and a CDN in front for
    # the rest (Volumes are commit/reload storage, not a cache many containers write at once).
    "XI_CACHE_RESULT_STORES": "memory",
    "XI_CACHE_RESULT_MEMORY_BYTES": "256MiB",
    # Workers follow GOMAXPROCS. Modal does not document a cgroup CPU quota or memory.max inside
    # the container, so pin both to the cpu= and memory= limits below instead of relying on the
    # server's cgroup detection: 2 workers, and GOMEMLIMIT at 30 % of 4 GiB.
    "GOMAXPROCS": "2",
    "GOMEMLIMIT": "1228MiB",
    # Modal does not probe /readyz and stops routing on its own: no load-balancer delay, and the
    # drain fits in the 30 s @modal.exit grace period.
    "XI_SERVER_SHUTDOWN_DELAY": "0s",
    "XI_SERVER_SHUTDOWN_TIMEOUT": "20s",
    # Admin listener (/metrics, pprof) stays on loopback: Modal exposes only the web_server port.
    # Push traces instead: put OTEL_EXPORTER_OTLP_ENDPOINT (and _HEADERS) in a Modal Secret and
    # add it to secrets=[...]. Logs are JSON on stdout, shown by `modal app logs`.
}

VIDEO_ENV = {
    "XI_ROLE": "video",
    "XI_VIDEO_TRANSFORM_ENABLED": "true",
    "XI_VIDEO_TRANSFORM_UNSIGNED_POLICY": "deny",
    # A dedicated video container: the video pool gets every core (OPERATIONS.md §2.2).
    "XI_VIDEO_TRANSFORM_WORKERS": "4",
    # Below Modal's 150 s HTTP request limit (this is the default, set here as a reminder): past
    # 150 s Modal answers for the server (a 303 to a result URL on Modal's web functions).
    "XI_VIDEO_TRANSFORM_TIMEOUT": "2m",
    # The spool and the disk cache live on the container's ephemeral disk (ephemeral_disk below),
    # created and handed to UID 65532 by start_server(). Lost when the container stops.
    "XI_VIDEO_TRANSFORM_SPOOL_DIR": "/var/tmp/xi/spool",
    "XI_VIDEO_TRANSFORM_SPOOL_MAX_BYTES": "16GiB",
    "XI_CACHE_RESULT_STORES": "disk",
    "XI_CACHE_RESULT_DISK_PATH": "/var/tmp/xi/cache",
    "XI_CACHE_RESULT_DISK_BYTES": "16GiB",
    "XI_SOURCES": "web",
    "XI_SOURCES_WEB_TYPE": "http",
    "XI_SOURCES_WEB_ALLOW": "media.example.com",
    "GOMAXPROCS": "4",
    "GOMEMLIMIT": "2457MiB",
    "XI_SERVER_SHUTDOWN_DELAY": "0s",
    # Modal gives @modal.exit 30 s, not the 130 s other platforms give a video drain: a transcode
    # still running after 25 s is cut and its client retries.
    "XI_SERVER_SHUTDOWN_TIMEOUT": "25s",
}

# Directories start_server() creates for UID 65532 (the root filesystem belongs to root on Modal).
WRITABLE = ("XI_VIDEO_TRANSFORM_SPOOL_DIR", "XI_CACHE_RESULT_DISK_PATH")


def start_server() -> subprocess.Popen[bytes]:
    """Start `xtandard-image serve` as UID 65532 with this container's environment."""
    env = {k: v for k, v in os.environ.items() if not k.startswith("MODAL_")}  # no Modal tokens
    for key in WRITABLE:
        if path := env.get(key):
            os.makedirs(path, exist_ok=True)
            os.chown(path, UID, GID)
    return subprocess.Popen([SERVER, "serve"], env=env, user=UID, group=GID, extra_groups=[], cwd="/tmp")


def stop_server(proc: subprocess.Popen[bytes], timeout: float) -> int | None:
    """SIGTERM: the server stops accepting, drains in-flight requests and exits."""
    if proc.poll() is None:
        proc.send_signal(signal.SIGTERM)
        try:
            proc.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            proc.kill()
    return proc.returncode


app = modal.App("xtandard-image")


@app.cls(
    image=server_image("slim"),
    env=IMAGE_ENV,
    secrets=[signing],
    # (request, limit): a hard 2-core CPU limit and a 4 GiB memory limit (OOM kill above it).
    # Modal bills the higher of request and use, per second; a Modal core is a physical core.
    cpu=(2.0, 2.0),
    memory=(4096, 4096),
    # Containers: 1 always warm (0 scales to zero: about a second to boot plus the image pull on
    # a cold start), at most 20, and 1 idle spare while traffic flows so a burst does not wait.
    min_containers=1,
    max_containers=20,
    buffer_containers=1,
    # Idle seconds before a container stops (2 s to 20 min, default 60): slow scale-in.
    scaledown_window=300,
    # Per request (input) execution limit; Modal cuts HTTP requests at 150 s anyway.
    timeout=150,
)
# Requests per container: the autoscaler targets 8 in flight (4 x workers, docs/DEPLOY.md "Picking
# the scaling signal") and lets one burst to 32, just under workers + queue (2 + 2 x 16), so Modal
# queues or scales out before the server's own 429.
@modal.concurrent(max_inputs=32, target_inputs=8)
class ImageFleet:
    @modal.enter()
    def start(self) -> None:
        self.proc = start_server()

    # Modal waits up to startup_timeout for the port to accept connections, then routes to it.
    @modal.web_server(PORT, startup_timeout=30, label="xtandard-image")
    def serve(self) -> None:
        pass

    @modal.exit()
    def stop(self) -> None:
        stop_server(self.proc, timeout=25)


@app.cls(
    image=server_image("full"),
    env=VIDEO_ENV,
    secrets=[signing, stream],
    cpu=(4.0, 4.0),
    memory=(8192, 8192),
    # 40 GiB of container disk for the spool and the cache (Modal raises the billed memory to
    # 1/20 of the disk, 2 GiB here, below the 8 GiB already requested).
    ephemeral_disk=40 * 1024,
    min_containers=0,  # scale to zero: video tolerates a cold start
    max_containers=10,
    buffer_containers=0,
    scaledown_window=300,
    timeout=150,
)
# Transcodes are heavy: about 2 in flight per container (XI_VIDEO_TRANSFORM_MAX_JOBS defaults
# to 2 x video workers); manifests and segments are cheap and share the limit.
@modal.concurrent(max_inputs=16, target_inputs=4)
class VideoFleet:
    @modal.enter()
    def start(self) -> None:
        self.proc = start_server()

    @modal.web_server(PORT, startup_timeout=60, label="xtandard-video")
    def serve(self) -> None:
        pass

    @modal.exit()
    def stop(self) -> None:
        stop_server(self.proc, timeout=28)
