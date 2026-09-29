#!/bin/sh
# Prints the base image references of the app image, derived from
# docker/base/versions.sh (the single source of truth for the base pin) and,
# for the full and gpl flavors, the revision in versions-full.sh /
# versions-gpl.sh.
#
# Usage:
#   scripts/base-image-ref.sh runtime            ghcr.io/…-base:<vips>-r<N>-slim
#   scripts/base-image-ref.sh builder            ghcr.io/…-base:<vips>-r<N>-slim-builder
#   scripts/base-image-ref.sh --digest runtime   …-slim@sha256:… (resolved on the registry)
#   scripts/base-image-ref.sh --flavor gpl [--digest] runtime|builder
#                                                …-base:<vips>-r<G>-gpl[-builder][@sha256:…]
#   scripts/base-image-ref.sh --check            fail if the Dockerfile ARG defaults drift
#
# XI_BASE_REPO overrides the repository (default ghcr.io/xantiagoma/xtandard-image-base).
set -eu
cd "$(dirname "$0")/.."

# shellcheck source=docker/base/versions.sh
. docker/base/versions.sh
repo="${XI_BASE_REPO:-ghcr.io/xantiagoma/xtandard-image-base}"

flavor="$XI_BASE_FLAVOR"
if [ "${1:-}" = --flavor ]; then
  flavor="${2:?usage: base-image-ref.sh --flavor slim|full|gpl ...}"
  shift 2
fi
# versions-full.sh and versions-gpl.sh are bash; only their revision line is read.
case "$flavor" in
  slim) revision="$XI_BASE_REVISION" ;;
  full) revision="$(sed -n 's/^export XI_BASE_FULL_REVISION=//p' docker/base/versions-full.sh)" ;;
  gpl) revision="$(sed -n 's/^export XI_BASE_GPL_REVISION=//p' docker/base/versions-gpl.sh)" ;;
  *) echo "base-image-ref: unknown flavor '$flavor' (slim|full|gpl)" >&2; exit 2 ;;
esac
[ -n "$revision" ] || { echo "base-image-ref: no revision for the $flavor flavor" >&2; exit 1; }
tag="${VIPS_VERSION}-r${revision}-${flavor}"

ref() {
  case "$1" in
    runtime) echo "$repo:$tag" ;;
    builder) echo "$repo:$tag-builder" ;;
    *) echo "base-image-ref: unknown target '$1' (runtime|builder)" >&2; exit 2 ;;
  esac
}

case "${1:-}" in
  --check)
    status=0
    for target in builder runtime; do
      want="$(ref "$target")"
      arg="BASE_$(echo "$target" | tr '[:lower:]' '[:upper:]')"
      got="$(sed -n "s/^ARG $arg=//p" Dockerfile)"
      if [ "$got" != "$want" ]; then
        echo "base-image-ref: Dockerfile ARG $arg=$got, versions.sh says $want" >&2
        status=1
      fi
    done
    [ "$status" -eq 0 ] && echo "base-image-ref: Dockerfile defaults match versions.sh ($tag)"
    exit "$status"
    ;;
  --digest)
    image="$(ref "${2:?usage: base-image-ref.sh [--flavor f] --digest runtime|builder}")"
    digest="$(docker buildx imagetools inspect "$image" | awk '/^Digest:/ { print $2; exit }')"
    [ -n "$digest" ] || { echo "base-image-ref: cannot resolve $image" >&2; exit 1; }
    echo "$image@$digest"
    ;;
  *)
    ref "${1:?usage: base-image-ref.sh [--flavor slim|full|gpl] [--digest] runtime|builder | --check}"
    ;;
esac
