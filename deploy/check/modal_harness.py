"""Runs deploy/modal/app.py's server lifecycle the way a Modal container would, without Modal.

  python -I modal_harness.py <app.py> <image|video>

Inside an image built like Modal's (deploy/check/modal-run.sh: the xtandard-image runtime plus a
standalone CPython, ENTRYPOINT cleared, running as root): applies the fleet's env dict and dummy
secrets, calls start_server() as @modal.enter would, waits for /readyz, checks that the server
runs as UID 65532 without MODAL_* variables and owns its writable directories, then calls
stop_server() as @modal.exit would and expects a clean exit within the grace period.
"""

from __future__ import annotations

import os
import runpy
import subprocess
import sys
import time
import urllib.error
import urllib.request

HEX = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"


def readyz(timeout: float) -> int:
    deadline = time.monotonic() + timeout
    code = 0
    while time.monotonic() < deadline:
        try:
            with urllib.request.urlopen("http://127.0.0.1:8080/readyz", timeout=2) as r:  # noqa: S310
                return r.status
        except urllib.error.HTTPError as e:
            code = e.code
        except OSError:
            pass
        time.sleep(0.2)
    return code


def main(app_path: str, fleet: str) -> int:
    mod = runpy.run_path(app_path)
    os.environ.update(mod[f"{fleet.upper()}_ENV"])
    os.environ.update(XI_SIGNING_KEYS=HEX, XI_SIGNING_SALTS=HEX, XI_VIDEO_TRANSFORM_STREAM_KEYS=HEX)
    os.environ["MODAL_TASK_ID"] = "ta-harness"  # Modal sets MODAL_* in every container
    errs: list[str] = []
    seen: dict[str, str] = {}
    popen = subprocess.Popen

    def record(*args, **kwargs):  # the server is not dumpable: /proc/<pid>/environ is closed
        seen.update(kwargs.get("env") or {})
        return popen(*args, **kwargs)

    subprocess.Popen = record  # type: ignore[misc]
    proc = mod["start_server"]()
    try:
        code = readyz(60)
        if code != 200:
            errs.append(f"/readyz answered {code}, want 200")
        status = open(f"/proc/{proc.pid}/status").read()
        uid = next(line.split()[1] for line in status.splitlines() if line.startswith("Uid:"))
        if uid != str(mod["UID"]):
            errs.append(f"server runs as UID {uid}, want {mod['UID']}")
        if not seen or any(k.startswith("MODAL_") for k in seen):
            errs.append("MODAL_* variables reached the server")
        for key in mod["WRITABLE"]:
            if path := os.environ.get(key):
                if os.stat(path).st_uid != mod["UID"]:
                    errs.append(f"{path} not owned by {mod['UID']}")
    finally:
        started = time.monotonic()
        rc = mod["stop_server"](proc, timeout=28)
        took = time.monotonic() - started
    if rc != 0:
        errs.append(f"server exited {rc} after SIGTERM, want 0")
    for e in errs:
        print(f"FAIL {fleet}: {e}")
    if not errs:
        print(f"harness ok {fleet}: readyz 200, uid {mod['UID']}, no MODAL_* env, clean stop in {took:.1f}s")
    return 1 if errs else 0


if __name__ == "__main__":
    if len(sys.argv) != 3 or sys.argv[2] not in ("image", "video"):
        print(__doc__)
        sys.exit(2)
    sys.exit(main(sys.argv[1], sys.argv[2]))
