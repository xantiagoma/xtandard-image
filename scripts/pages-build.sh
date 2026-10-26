#!/bin/sh
# Assembles the GitHub Pages site (WP-81b, ADR 0007 decision 6) into
# dist/pages/ (or $1). Used by `task pages:build` and
# .github/workflows/pages.yml, so the deployed site can be checked locally.
#
#   index.html                          links every file (docs/pages/index.html)
#   api/index.html                      Scalar over schema/latest/openapi.json
#   schema/<major>/config.schema.json   the config schema; <major> comes from its $id
#   schema/<major>/openapi.json         the OpenAPI document
#   schema/latest/…                     the same two files
#   assets/…                            favicons, header lockups, social.png (docs/assets)
#
# Deterministic: the output depends only on the committed files (no dates,
# no commit ids), and it fails when the $id is not a Pages URL of the form
# .../schema/v<N>/config.schema.json.
set -eu
cd "$(dirname "$0")/.."

out="${1:-dist/pages}"
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
sed "s/@MAJOR@/$major/g" docs/pages/index.html >"$out/index.html"
cp docs/pages/api/index.html "$out/api/index.html"
# Brand files the pages reference (favicons, header lockup, og:image; docs/BRAND.md).
mkdir -p "$out/assets"
for f in favicon.svg favicon.ico apple-touch-icon.png social.png lockup-horizontal-dark.svg lockup-horizontal-light.svg; do
  cp "docs/assets/$f" "$out/assets/$f"
done
# Serve files as they are (no Jekyll processing of the artifact).
: >"$out/.nojekyll"

if grep -q '@MAJOR@' "$out/index.html"; then
  echo "pages-build: unreplaced @MAJOR@ in index.html" >&2
  exit 1
fi
echo "pages-build: $out (schema $major, \$id $id)"
(cd "$out" && find . -type f | LC_ALL=C sort)
