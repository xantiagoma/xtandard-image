#!/bin/sh
# Rebuilds every brand raster in docs/assets/ from the SVG masters next to
# them, and copies the playground's favicon set into
# internal/playground/assets/brand/ (WP-153, docs/BRAND.md).
#
#   sh scripts/brand-assets.sh           rebuild (task brand:assets)
#   sh scripts/brand-assets.sh --check   fail when a raster or master is
#                                        missing, or the playground copies
#                                        differ from docs/assets (task brand:check)
#
# Needs rsvg-convert (librsvg) and magick (ImageMagick 7); oxipng is used
# when present (brew install librsvg imagemagick oxipng). The SVGs have no
# font dependency (text is outlined), so the output does not depend on the
# fonts installed on the machine; it can differ by a few bytes between
# librsvg versions, and checkouts do not keep mtimes, so --check compares
# neither pixels nor timestamps: rerun the script after editing a master.
set -eu
cd "$(dirname "$0")/.."

a=docs/assets
pg=internal/playground/assets/brand

# target master [rsvg-convert args...]: every raster and the master it comes from.
rasters='
apple-touch-icon.png logo.svg
social.png social.svg -w 1280 -h 640
favicon.ico favicon.svg
'
# symbol-light.svg is symbol.svg with the light-background peak stop the light
# lockups use, so the front mountain does not wash out on white.
light_symbol() { sed 's/<stop offset="0" stop-color="#E6F4FD"\/>/<stop offset="0" stop-color="#C4E4FB"\/>/' "$a/symbol.svg"; }
# The playground serves these from its embedded FS (CSP default-src 'self').
pgfiles='favicon.svg favicon.ico apple-touch-icon.png symbol.svg symbol-light.svg'

if [ "${1:-}" = "--check" ]; then
  status=0
  printf '%s\n' "$rasters" | while read -r out src _; do
    [ -n "$out" ] || continue
    if [ ! -f "$a/$out" ] || [ ! -f "$a/$src" ]; then
      echo "brand-assets: $a/$out or its master $a/$src is missing (run task brand:assets)" >&2
      exit 1
    fi
  done || status=1
  if ! light_symbol | cmp -s - "$a/symbol-light.svg"; then
    echo "brand-assets: $a/symbol-light.svg is not current with symbol.svg (run task brand:assets)" >&2
    status=1
  fi
  for f in $pgfiles; do
    if ! cmp -s "$a/$f" "$pg/$f"; then
      echo "brand-assets: $pg/$f differs from $a/$f (run task brand:assets)" >&2
      status=1
    fi
  done
  exit "$status"
fi

command -v rsvg-convert >/dev/null || { echo "brand-assets: rsvg-convert not found (brew install librsvg)" >&2; exit 1; }
command -v magick >/dev/null || { echo "brand-assets: magick not found (brew install imagemagick)" >&2; exit 1; }

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

printf '%s\n' "$rasters" | while read -r out src args; do
  [ -n "$out" ] || continue
  case "$out" in
    favicon.ico)
      for s in 16 32 48; do rsvg-convert -w "$s" -h "$s" "$a/$src" -o "$tmp/fav-$s.png"; done
      magick "$tmp/fav-16.png" "$tmp/fav-32.png" "$tmp/fav-48.png" "$a/$out"
      ;;
    apple-touch-icon.png)
      # iOS masks the icon itself and shows transparency as black: render it
      # full-bleed on the icon's background colour, no alpha.
      rsvg-convert -w 180 -h 180 "$a/$src" -o "$tmp/touch.png"
      magick "$tmp/touch.png" -background '#0F1215' -flatten -alpha off "$a/$out"
      ;;
    *)
      # shellcheck disable=SC2086 # args is a word list on purpose
      rsvg-convert $args "$a/$src" -o "$a/$out"
      ;;
  esac
done

if command -v oxipng >/dev/null; then
  oxipng -q -o max --strip safe "$a"/*.png
else
  echo "brand-assets: oxipng not found, PNGs left unoptimised" >&2
fi

light_symbol >"$a/symbol-light.svg"

mkdir -p "$pg"
for f in $pgfiles; do cp "$a/$f" "$pg/$f"; done
ls -l "$a" "$pg"
