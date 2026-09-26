#!/bin/sh
# Shallow-clone reference codebases for grep-driven research. Re-runnable.
set -u
cd "$(dirname "$0")/../.refs" 2>/dev/null || { mkdir -p "$(dirname "$0")/../.refs" && cd "$(dirname "$0")/../.refs"; }
c() { # c <dir> <url>
  [ -d "$1" ] && { echo "skip $1"; return; }
  git clone -q --depth 1 "$2" "$1" && echo "ok   $1" || echo "FAIL $1"
}
# Open-source image proxies
c imgproxy               https://github.com/imgproxy/imgproxy
c imgproxy-docker-base   https://github.com/imgproxy/imgproxy-docker-base
c imgproxy-js-core       https://github.com/imgproxy/imgproxy-js-core
c imagor                 https://github.com/cshum/imagor
c vipsgen                https://github.com/cshum/vipsgen
c imagor-base            https://github.com/cshum/imagor-base
c thumbor                https://github.com/thumbor/thumbor
c imaginary              https://github.com/h2non/imaginary
c bimg                   https://github.com/h2non/bimg
c weserv-images          https://github.com/weserv/images
c imageproxy             https://github.com/willnorris/imageproxy
c pilbox                 https://github.com/agschwender/pilbox
c picfit                 https://github.com/thoas/picfit
c govips                 https://github.com/davidbyttow/govips
# Engines
c libvips                https://github.com/libvips/libvips
c sharp                  https://github.com/lovell/sharp
c sharp-libvips          https://github.com/lovell/sharp-libvips
c libraw                 https://github.com/LibRaw/LibRaw
c libheif                https://github.com/strukturag/libheif
c pigo                   https://github.com/esimov/pigo
# URL builders / provider SDKs (to reverse the URL grammars)
c unpic                  https://github.com/ascorbic/unpic
c unpic-img              https://github.com/ascorbic/unpic-img
c imgix-js-core          https://github.com/imgix/js-core
c imgix-blueprint        https://github.com/imgix/imgix-blueprint
c cloudinary-url-gen     https://github.com/cloudinary/js-url-gen
c cloudinary-npm         https://github.com/cloudinary/cloudinary_npm
c imagekit-javascript    https://github.com/imagekit-developer/imagekit-javascript
c imagekit-nodejs        https://github.com/imagekit-developer/imagekit-nodejs
c bunny-sdk              https://github.com/BunnyWay/BunnySDK
c thumbor-url            https://github.com/thumbor/thumbor-url  
c aws-image-handler      https://github.com/aws-solutions/dynamic-image-transformation-for-amazon-cloudfront
c sharp-docs             https://github.com/lovell/sharp
# Cloudflare Images docs, sparse checkout only the images section
if [ ! -d cloudflare-docs ]; then
  git clone -q --depth 1 --filter=blob:none --sparse https://github.com/cloudflare/cloudflare-docs cloudflare-docs \
    && (cd cloudflare-docs && git sparse-checkout set src/content/docs/images) && echo "ok   cloudflare-docs" || echo "FAIL cloudflare-docs"
fi
du -sh . | tail -1
