#!/usr/bin/env bash
# shellcheck disable=SC2015  # ok() never fails: `check && ok || bad` is an if-then-else here.
# Local stand-in for a Modal container (deploy/modal/app.py), with no Modal account: builds what
# Modal's Image.from_registry(..., add_python="3.13").entrypoint([]) builds (the runtime image, a
# standalone CPython copied into /usr/local, ENTRYPOINT cleared, run as root because Modal
# ignores USER), installs the pinned modal client, and runs deploy/check/modal_harness.py for
# both fleets: the app's own start_server()/stop_server() with its env dicts, readiness, UID,
# environment and a clean SIGTERM drain. It does not exercise Modal's proxy or autoscaler.
#
#   XI_IMAGE=xtandard-image:main-gpl deploy/check/modal-run.sh
#
# Env: XI_IMAGE (default xtandard-image:main-gpl; full or gpl so the video fleet starts).
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
image="${XI_IMAGE:-xtandard-image:main-gpl}"
cache="${DEPLOY_CHECK_CACHE:-$root/deploy/.cache}/modal"
# Pinned: the modal client of deploy/modal (docs/DEPLOY.md "Modal") and the uv that fetches a
# python-build-standalone CPython, the distribution add_python installs.
modal_version="1.6.2"
python_version="3.13"
uv_image="ghcr.io/astral-sh/uv:0.9.5-python3.13-bookworm-slim"
tag="xtandard-image-modal-check:local"
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

docker image inspect "$image" >/dev/null 2>&1 || { echo "image $image not present (set XI_IMAGE)"; exit 1; }
mkdir -p "$cache"
if [[ ! -x "$cache/python/bin/python3" ]]; then
  rm -rf "$cache/python" "$cache/uv-python"
  docker run --rm -u "$(id -u):$(id -g)" -e HOME=/tmp -e UV_CACHE_DIR=/tmp/uv \
    -e UV_PYTHON_INSTALL_DIR=/out/uv-python -v "$cache:/out" "$uv_image" \
    uv python install -q "$python_version"
  mv "$cache"/uv-python/cpython-"$python_version".*-linux-*-gnu "$cache/python"
  rm -rf "$cache/uv-python"
fi
cp deploy/modal/app.py deploy/check/modal_harness.py "$cache/"

docker build -q -t "$tag" -f - "$cache" >/dev/null <<EOF
FROM $image
USER root
COPY python/. /usr/local
ENV TERMINFO_DIRS=/etc/terminfo:/lib/terminfo:/usr/share/terminfo:/usr/lib/terminfo
RUN ["/usr/local/bin/python3", "-m", "pip", "install", "-q", "--break-system-packages", "--root-user-action=ignore", "--no-cache-dir", "modal==$modal_version"]
COPY app.py modal_harness.py /check/
ENTRYPOINT []
EOF
ok "modal-like image $tag from $image (CPython $python_version, modal $modal_version)"

for fleet in image video; do
  if out="$(docker run --rm "$tag" python -I /check/modal_harness.py /check/app.py "$fleet" 2>&1)"; then
    grep -q '"msg":"go memory limit from GOMEMLIMIT"' <<<"$out" \
      && ok "$fleet fleet: GOMEMLIMIT from the app's env is used" || bad "$fleet fleet: no GOMEMLIMIT log line"
    ok "$(grep '^harness ok' <<<"$out")"
  else
    bad "$fleet fleet: $(grep -E '^FAIL|level":"ERROR' <<<"$out" || tail -5 <<<"$out")"
  fi
done
docker image rm -f "$tag" >/dev/null
exit "$fail"
