#!/bin/sh
# Regenerates the designer fixtures that come from the applications themselves
# (WP-107): testdata/fixtures/xcf/ (GIMP 2.10), ora/ and kra/ (Krita 5.2), and
# GIMP's flattened renders in testdata/golden/codec/xcf-*-gimp.png. Run it on
# the host after gen.sh (it reads the CC0 photo, logo and layered PSD fixtures):
#
#   sh testdata/gen/designer.sh
#
# Requirements: Docker. GIMP and Krita (both GPL) run in a throwaway
# ubuntu:24.04 container; they only encode our pixels, nothing of theirs ends
# up in the files (testdata/LICENSES.md). GIMP's batch script is
# testdata/gen/designer-gimp.scm. Krita imports the layered PSD fixture (a
# photo and a graphic at an offset) and exports it as .kra and .ora; its XCF
# importer does not read GIMP 2.10 files. Outputs depend on the application
# versions; tests compare them perceptually.
set -eu
cd "$(dirname "$0")/../.."

F=testdata/fixtures
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

cp "$F/jpeg/baseline.jpg" "$F/watermark/logo.png" "$F/psd/layered.psd" "$T/"
cp testdata/gen/designer-gimp.scm "$T/mk.scm"
docker run --rm -v "$T:/fx" ubuntu:24.04 sh -euc '
	apt-get update -qq
	DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends gimp krita xvfb xauth >/dev/null
	cd /fx
	gimp -i -d -f -b "$(cat mk.scm)"
	export HOME=/tmp/krita
	xvfb-run -a krita --nosplash --export --export-filename /fx/layers.kra /fx/layered.psd
	xvfb-run -a krita --nosplash --export --export-filename /fx/layers.ora /fx/layered.psd
'
mkdir -p "$F/xcf" "$F/ora" "$F/kra" testdata/golden/codec
for n in layers gray alpha; do
	cp "$T/$n.xcf" "$F/xcf/$n.xcf"
	cp "$T/$n-flat.png" "testdata/golden/codec/xcf-$n-gimp.png"
done
cp "$T/layers.ora" "$F/ora/layers.ora"
cp "$T/layers.kra" "$F/kra/layers.kra"
