#!/bin/sh
# Assembles the static, non-Astro files of the GitHub Pages site (WP-81b,
# ADR 0007 decision 6; WP-156, ADR 0015) into dist/pages-static/ (or $1).
# The documentation site (site/, `task pages:build`) uses that directory as
# Astro's publicDir, so these files reach dist/pages/ byte for byte; the
# verify job of .github/workflows/pages.yml runs this script alone to get
# the JSON files it compares with the published ones.
#
#   api/index.html                      Scalar over schema/latest/openapi.json
#   schema/<major>/config.schema.json   the config schema; <major> comes from its $id
#   schema/<major>/openapi.json         the OpenAPI document
#   schema/latest/…                     the same two files
#   assets/…                            every file of docs/assets (favicons, lockups, social.png, screenshots)
#
# Deterministic: the output depends only on the committed files (no dates,
# no commit ids), and it fails when the $id is not a Pages URL of the form
# .../schema/v<N>/config.schema.json.
set -eu
# $1 is relative to the caller's directory; the default to the repository.
case "${1:-}" in
"") out= ;;
/*) out="$1" ;;
*) out="$(pwd)/$1" ;;
esac
cd "$(dirname "$0")/.."
out="${out:-dist/pages-static}"
base="https://xantiagoma.github.io/xtandard-image/schema/"
schema="internal/config/config.schema.json"
openapi="internal/apidoc/openapi.json"

id="$(sed -n 's/^  "\$id": "\(.*\)",\{0,1\}$/\1/p' "$schema")"
major="${id#"$base"}"
major="${major%/config.schema.json}"
if [ "$base$major/config.schema.json" != "$id" ] || ! printf '%s\n' "$major" | grep -Eq '^v[1-9][0-9]*$'; then
  echo "pages-build: $schema \$id '$id' is not ${base}v<N>/config.schema.json" >&2
  exit 1
fi

rm -rf "$out"
mkdir -p "$out/api" "$out/schema/$major" "$out/schema/latest"
for dir in "$major" latest; do
  cp "$schema" "$out/schema/$dir/config.schema.json"
  cp "$openapi" "$out/schema/$dir/openapi.json"
done
cp docs/pages/api/index.html "$out/api/index.html"
# Brand files and screenshots (favicons, lockups, og:image, the playground
# screenshots the docs show; docs/BRAND.md).
mkdir -p "$out/assets"
cp docs/assets/* "$out/assets/"
# Serve files as they are (no Jekyll processing of the artifact).
: >"$out/.nojekyll"

echo "pages-build: $out (schema $major, \$id $id)"
(cd "$out" && find . -type f | LC_ALL=C sort)
