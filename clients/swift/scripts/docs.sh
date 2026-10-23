#!/bin/sh
# Builds the DocC documentation of XtandardImage with warnings as errors
# (unresolved symbol links, undocumented parameters). It uses the docc binary
# of the toolchain (Xcode on macOS, the swift image on Linux) on the module's
# symbol graph, so the package needs no swift-docc-plugin dependency.
#
#   scripts/docs.sh [scratch path]     # default .build; output in <scratch>/docs
set -eu
cd "$(dirname "$0")/.."
scratch="${1:-.build}"
swift="${XI_SWIFT:-swift}"

$swift package --package-path ../.. --scratch-path "$scratch" dump-symbol-graph --minimum-access-level public >/dev/null
graphs="$scratch/symbol-graphs"
rm -rf "$graphs"
mkdir -p "$graphs"
# Only this module's graphs (the test target and dependencies are not documented).
find "$scratch" -path '*/symbolgraph/XtandardImage*.symbols.json' -exec cp {} "$graphs/" \;

if command -v xcrun >/dev/null 2>&1; then docc="xcrun docc"; else docc="docc"; fi
$docc convert Sources/XtandardImage/XtandardImage.docc \
  --fallback-display-name XtandardImage \
  --fallback-bundle-identifier io.github.xantiagoma.XtandardImage \
  --additional-symbol-graph-dir "$graphs" \
  --output-path "$scratch/docs" \
  --warnings-as-errors
echo "documentation: $scratch/docs"
