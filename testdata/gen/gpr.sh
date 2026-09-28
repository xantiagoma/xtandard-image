#!/bin/sh
# Regenerates testdata/fixtures/gpr/hero11-crop.gpr (WP-103): a 256x192 GoPro
# GPR (VC-5 compressed DNG) cut from the CC0 raw.pixls.us HERO11 Black sample
# (id 7319). GoPro's gpr_tools decodes the sample to raw 14-bit RGGB, a
# 256x192 window at (2400, 2000) is copied out (even offsets keep the CFA
# phase), and gpr_tools encodes it again with the sample's own parameters
# (colour matrices, black/white levels, gain map) and an empty serial number.
# Run it on the host:
#
#   sh testdata/gen/gpr.sh
#
# Requirements: curl and Docker (ubuntu:noble). The GPR SDK (Apache-2.0 OR
# MIT, with its own old DNG and XMP SDKs) is built from the commit pinned in
# docker/base/versions-gpl.sh and only run here.
set -eu
cd "$(dirname "$0")/../.."

F=testdata/fixtures/gpr
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
SAMPLE_URL="https://raw.pixls.us/getfile.php/7319/nice/GoPro%20-%20HERO11%20Black%20-%2016bit%20%285%3A4%29.GPR"
SAMPLE_SHA256=d7a6be163c847cb8456e6858a99aef3b85dc3d8836bca1da1b8886effe0d43e1

# shellcheck source=../../docker/base/versions-gpl.sh
. docker/base/versions-gpl.sh
curl -fsSL -o "$T/gpr.tar.gz" "https://github.com/gopro/gpr/archive/${GPR_COMMIT}.tar.gz"
curl -fsSL -o "$T/hero11.gpr" "$SAMPLE_URL"
sum() { (sha256sum "$1" 2>/dev/null || shasum -a 256 "$1") | cut -d' ' -f1; }
[ "$(sum "$T/gpr.tar.gz")" = "$GPR_SHA256" ] || { echo "GPR SDK checksum mismatch" >&2; exit 1; }
[ "$(sum "$T/hero11.gpr")" = "$SAMPLE_SHA256" ] || { echo "sample checksum mismatch" >&2; exit 1; }

docker run --rm -v "$T:/t" -w /t ubuntu:noble sh -eu -c '
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends g++ make cmake python3 >/dev/null 2>&1
mkdir gpr && tar -xzf gpr.tar.gz -C gpr --strip-components=1
cd gpr && mkdir build && cd build
# The bundled DNG SDK 1.4 cannot detect the byte order on arm64.
cmake -DCMAKE_BUILD_TYPE=Release -DCMAKE_CXX_FLAGS="-DqDNGLittleEndian=1 -w" \
  -DCMAKE_C_FLAGS=-w .. >/dev/null
make -j"$(nproc)" gpr_tools >/dev/null
T=/t/gpr/build/source/app/gpr_tools/gpr_tools
cd /t
$T -i hero11.gpr -o hero11.raw >/dev/null
$T -i hero11.gpr -d 1 > params.txt
python3 - <<EOF
W, H, X, Y, SW, SH = 256, 192, 2400, 2000, 5568, 4872
src = open("hero11.raw", "rb").read()
pitch = len(src) // SH
out = bytearray()
for y in range(Y, Y + H):
    out += src[y * pitch + X * 2 : y * pitch + (X + W) * 2]
open("crop.raw", "wb").write(out)
p = open("params.txt").read()
for old, new in (("\"input_width\": 5568", "\"input_width\": %d" % W),
                 ("\"input_height\": 4872", "\"input_height\": %d" % H),
                 ("\"input_pitch\": %d" % pitch, "\"input_pitch\": %d" % (W * 2))):
    assert old in p, old
    p = p.replace(old, new)
import re
p = re.sub(r"\"camera_serial\": \"[^\"]*\"", "\"camera_serial\": \"\"", p)
open("params-crop.txt", "w").write(p)
EOF
$T -i crop.raw -o crop.gpr -a params-crop.txt -w 256 -h 192 -p 512 -x rggb14 >/dev/null
'
mkdir -p "$F"
cp "$T/crop.gpr" "$F/hero11-crop.gpr"
echo "wrote $F/hero11-crop.gpr"
