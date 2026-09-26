#!/bin/bash
# License gate (ADR 0006). Fails when a forbidden (GPL/AGPL) component shows up in
# the built library directory, in the pinned versions files, in the FFmpeg build
# configuration, in the resolved shared-library dependencies, or when a shipped model
# lacks a permissive licence.
#
# Usage: check-licenses.sh [--ffmpeg <ffmpeg>] [--ldd] [--models <dir>] <lib-dir> [versions.sh ...]
#
# Checks:
#   1. file names under <lib-dir> (libx265.so*, poppler, exiv2, libimagequant, ...)
#   2. binary contents of every file under <lib-dir>: DT_NEEDED / SONAME strings of
#      forbidden libraries, their well-known entry points, and FFmpeg's
#      "--enable-gpl" / "--enable-nonfree" configure strings
#   3. pkg-config files under <lib-dir> requiring a forbidden package
#   4. every line of each versions file (a forbidden dependency must never be pinned)
#   --ffmpeg <bin>  (full flavor, WP-70) `<bin> -buildconf` has no --enable-gpl or
#                   --enable-nonfree and `<bin> -L` reports the GNU Lesser GPL
#   --ldd           every ELF file under <lib-dir> (and the --ffmpeg binary) is run
#                   through ldd ($LDD, default ldd): no resolved dependency may be a
#                   forbidden library, and none may be "not found"
#   --models <dir>  every <name>.onnx has a non-empty <name>.LICENSE that is MIT,
#                   Apache-2.0, BSD or ISC and not GPL/AGPL/SSPL/non-commercial;
#                   SHA256SUMS in <dir>, when present, must verify
#
# Exit status: 0 when clean, 1 when a forbidden component is found, 2 on usage error.

set -uo pipefail

usage() {
  echo "usage: $0 [--ffmpeg <ffmpeg>] [--ldd] [--models <dir>] <lib-dir> [versions.sh ...]" >&2
  exit 2
}

ffmpeg_bin=
use_ldd=0
models_dir=
while [ $# -gt 0 ]; do
  case "$1" in
    --ffmpeg) [ $# -ge 2 ] || usage; ffmpeg_bin=$2; shift 2 ;;
    --ldd) use_ldd=1; shift ;;
    --models) [ $# -ge 2 ] || usage; models_dir=$2; shift 2 ;;
    --) shift; break ;;
    -*) usage ;;
    *) break ;;
  esac
done
[ $# -ge 1 ] || usage

lib_dir=$1
shift
versions_files=("$@")

if [ ! -d "$lib_dir" ]; then
  echo "check-licenses: $lib_dir is not a directory" >&2
  exit 2
fi

# Component names that must never appear (case-insensitive):
#   x265, x264 (GPL encoders), poppler (GPL PDF), exiv2 (GPL metadata),
#   libimagequant (GPL-3 since 2.12; use quantizr), jbig-kit (GPL),
#   Ghostscript (AGPL), GPL FFmpeg builds and libpostproc (GPL-only FFmpeg library),
#   xvid (GPL MPEG-4 encoder), and the GPL delegates ImageMagick or LibRaw
#   could pick up (WP-66): FFTW, DjVuLibre, AutoTrace, LibRaw's GPL demosaic packs.
name_pattern='x265|x264|poppler|exiv2|imagequant|jbig|ghostscript|libgs\.so|ffmpeg-gpl|postproc|xvidcore|fftw|djvulibre|autotrace|demosaic[-_]pack[-_]gpl'

# Strings that only exist inside binaries built from or linked against those components.
content_pattern='libx265\.so|x265_encoder_open|libx264\.so|x264_encoder_open|libpoppler|poppler_document_new|libexiv2|Exiv2::|libimagequant\.so|libjbig|jbg_enc_init|libgs\.so|gsapi_new_instance|--enable-gpl|--enable-nonfree|--enable-libx26[45]|libpostproc\.so|libxvidcore\.so|libfftw3|fftw_plan_dft|libdjvulibre|ddjvu_document_create|libautotrace|at_splines_new'

failures=0

report() {
  echo "check-licenses: FORBIDDEN $1" >&2
  failures=$((failures + 1))
}

is_elf() {
  [ -f "$1" ] && [ "$(LC_ALL=C head -c 4 "$1" 2>/dev/null)" = $'\177ELF' ]
}

# 1. File names (relative to <lib-dir>, so the directory's own location never matches).
while IFS= read -r path; do
  if grep -qiE -- "$name_pattern" <<< "${path#"$lib_dir"}"; then
    report "file name: $path"
  fi
done < <(find "$lib_dir" -mindepth 1 -print)

# 2. Binary contents (covers DT_NEEDED entries without needing readelf).
while IFS= read -r path; do
  report "content of $path matches: $(grep -aoE -m1 -- "$content_pattern" "$path" | head -n1)"
done < <(find "$lib_dir" -type f -exec grep -laE -- "$content_pattern" {} + 2>/dev/null)

# 3. pkg-config requirements.
while IFS= read -r path; do
  report "pkg-config requirement in $path: $(grep -iE '^Requires' "$path" | grep -ioE "$name_pattern" | head -n1)"
done < <(find "$lib_dir" -type f -name '*.pc' -exec grep -ilE "^Requires.*($name_pattern)" {} + 2>/dev/null)

# 4. Versions files.
for versions_file in ${versions_files[@]+"${versions_files[@]}"}; do
  if [ ! -f "$versions_file" ]; then
    echo "check-licenses: $versions_file not found" >&2
    exit 2
  fi
  while IFS= read -r line; do
    report "pinned in $versions_file: $line"
  done < <(grep -inE -- "$name_pattern|enable-gpl|enable-nonfree" "$versions_file")
done

# 5. FFmpeg build configuration and licence (full flavor).
if [ -n "$ffmpeg_bin" ]; then
  if ! buildconf="$("$ffmpeg_bin" -hide_banner -buildconf 2>&1)"; then
    report "$ffmpeg_bin -buildconf failed: $buildconf"
  else
    while IFS= read -r flag; do
      report "ffmpeg configured with $flag"
    done < <(grep -oE -- '--enable-(gpl|nonfree|libx26[45]|libxvid|frei0r|librubberband|libvidstab|libzvbi|libcdio|libdavs2|libxavs2?)' <<< "$buildconf" | sort -u)
  fi
  ffmpeg_license="$("$ffmpeg_bin" -hide_banner -L 2>&1)"
  if ! grep -q 'GNU Lesser General Public' <<< "$ffmpeg_license"; then
    report "$ffmpeg_bin -L does not report the GNU Lesser General Public License"
  fi
fi

# 6. Resolved shared-library dependencies.
if [ "$use_ldd" = "1" ]; then
  ldd_cmd=${LDD:-ldd}
  elf_files=()
  while IFS= read -r path; do
    is_elf "$path" && elf_files+=("$path")
  done < <(find "$lib_dir" -type f)
  [ -n "$ffmpeg_bin" ] && elf_files+=("$(command -v "$ffmpeg_bin" || echo "$ffmpeg_bin")")
  for path in ${elf_files[@]+"${elf_files[@]}"}; do
    deps="$("$ldd_cmd" "$path" 2>&1)"
    while IFS= read -r line; do
      report "$path links $line"
    done < <(grep -iE "$name_pattern" <<< "$deps" | sed 's/^[[:space:]]*//')
    while IFS= read -r line; do
      report "$path has an unresolved dependency: $line"
    done < <(grep -F 'not found' <<< "$deps" | sed 's/^[[:space:]]*//')
  done
  echo "check-licenses: ldd checked ${#elf_files[@]} ELF file(s)"
fi

# 7. Model licences.
if [ -n "$models_dir" ]; then
  if [ ! -d "$models_dir" ]; then
    echo "check-licenses: $models_dir is not a directory" >&2
    exit 2
  fi
  allowed_model_license='MIT License|Permission is hereby granted, free of charge|Apache License|Redistribution and use in source and binary forms|ISC License'
  forbidden_model_license='GNU (Affero )?General Public License|AGPL|Server Side Public License|SSPL|non-?commercial|research (purposes )?only|CC-BY-NC|Attribution-NonCommercial'
  models=0
  while IFS= read -r model; do
    models=$((models + 1))
    license="${model%.onnx}.LICENSE"
    if [ ! -s "$license" ]; then
      report "model $model has no licence file ${license##*/}"
      continue
    fi
    if grep -qiE -- "$forbidden_model_license" "$license"; then
      report "model $model licence is not permissive: $(grep -oiE -m1 -- "$forbidden_model_license" "$license" | head -n1)"
    elif ! grep -qE -- "$allowed_model_license" "$license"; then
      report "model $model licence is not MIT/Apache-2.0/BSD/ISC"
    fi
  done < <(find "$models_dir" -type f -name '*.onnx' | sort)
  if [ "$models" -eq 0 ]; then
    report "no *.onnx model in $models_dir"
  fi
  if [ -f "$models_dir/SHA256SUMS" ]; then
    if command -v sha256sum > /dev/null; then
      sums_ok() { (cd "$models_dir" && sha256sum --quiet -c SHA256SUMS); }
    else
      sums_ok() { (cd "$models_dir" && shasum -a 256 --quiet -c SHA256SUMS); }
    fi
    if ! sums_ok >&2; then
      report "model checksums in $models_dir/SHA256SUMS do not match"
    fi
  fi
  echo "check-licenses: $models model(s) with permissive licences in $models_dir"
fi

if [ "$failures" -gt 0 ]; then
  echo "check-licenses: $failures forbidden component(s) found; see docs/adr/0006-license-policy.md" >&2
  exit 1
fi

extras=""
[ -n "$ffmpeg_bin" ] && extras+=", ffmpeg LGPL"
[ "$use_ldd" = "1" ] && extras+=", ldd"
[ -n "$models_dir" ] && extras+=", models"
echo "check-licenses: OK ($lib_dir${versions_files[*]+, ${versions_files[*]}}$extras)"
