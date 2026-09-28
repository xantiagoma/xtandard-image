#!/bin/bash
# License gate (ADR 0006). Fails when a forbidden (GPL/AGPL) component shows up in
# the built library directory, in the pinned versions files, in the FFmpeg build
# configuration, in the resolved shared-library dependencies, or when a shipped model
# lacks a permissive licence.
#
# Usage: check-licenses.sh [--allow-gpl <versions-gpl.sh>] [--licenses <dir>]
#          [--ffmpeg <ffmpeg>] [--ghostscript <gs>] [--ldd] [--models <dir>] <lib-dir> [versions.sh ...]
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
# gpl flavor only (WP-70b, ADR 0006 amendment 2026-09-27):
#   --allow-gpl <versions-gpl.sh>
#                   the components listed in its XI_GPL_COMPONENTS (component=SPDX;
#                   only x264, x265 and ffmpeg under GPL-2.0/GPL-3.0, ghostscript
#                   under exactly AGPL-3.0-or-later, WP-99, and dng-sdk, the Adobe DNG
#                   SDK inside libraw_r, under exactly LicenseRef-Adobe-DNG-SDK,
#                   WP-103, owner decision 2026-09-28, and libemf2svg under
#                   GPL-2.0, WP-108, EMF input) stop being forbidden: their
#                   names, symbols and pins pass, and --ffmpeg then requires
#                   --enable-gpl and `-L` reporting the GNU General Public License (and
#                   no --enable-version3 for a GPL-2.0 entry). Everything else stays
#                   forbidden: nonfree, any other AGPL, poppler, exiv2, libpostproc,
#                   and libgs (Ghostscript linked as a library) even with ghostscript
#                   allowed.
#   --licenses <dir>
#                   (needs --allow-gpl) every entry of the licence bundle <dir> has a
#                   licence in XI_COMPONENT_LICENSES made only of known identifiers (no
#                   SSPL or unknown ones; AGPL-3.0-or-later only for an allowed
#                   ghostscript), and <dir>/LICENSE-GPL.md exists and declares the
#                   combined GPL-2.0-or-later licence (and AGPL-3.0-or-later when
#                   ghostscript is allowed)
#   --ghostscript <gs>
#                   (needs ghostscript allowed) `<gs> --version` runs and `<gs> -h`
#                   lists exactly the bbox, nullpage, png16m and pngalpha devices; with
#                   --ldd, gs is checked like the libraries
#
# Exit status: 0 when clean, 1 when a forbidden component is found, 2 on usage error.

set -uo pipefail

usage() {
  echo "usage: $0 [--allow-gpl <versions-gpl.sh>] [--licenses <dir>] [--ffmpeg <ffmpeg>] [--ghostscript <gs>] [--ldd] [--models <dir>] <lib-dir> [versions.sh ...]" >&2
  exit 2
}

ffmpeg_bin=
gs_bin=
use_ldd=0
models_dir=
gpl_file=
licenses_dir=
while [ $# -gt 0 ]; do
  case "$1" in
    --allow-gpl) [ $# -ge 2 ] || usage; gpl_file=$2; shift 2 ;;
    --licenses) [ $# -ge 2 ] || usage; licenses_dir=$2; shift 2 ;;
    --ffmpeg) [ $# -ge 2 ] || usage; ffmpeg_bin=$2; shift 2 ;;
    --ghostscript) [ $# -ge 2 ] || usage; gs_bin=$2; shift 2 ;;
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
if [ -n "$licenses_dir" ] && [ -z "$gpl_file" ]; then
  echo "check-licenses: --licenses needs --allow-gpl" >&2
  exit 2
fi

# Component names that must never appear (case-insensitive):
#   x265, x264 (GPL encoders), poppler (GPL PDF), exiv2 (GPL metadata),
#   libimagequant (GPL-3 since 2.12; use quantizr), jbig-kit (GPL),
#   Ghostscript (AGPL), GPL FFmpeg builds and libpostproc (GPL-only FFmpeg library),
#   xvid (GPL MPEG-4 encoder), and the GPL delegates ImageMagick or LibRaw
#   could pick up (WP-66): FFTW, DjVuLibre, AutoTrace, LibRaw's GPL demosaic packs;
#   and the Adobe DNG SDK (not GPL, but its licence is confined to the gpl flavor).
# Each item is `regex` or `regex=component`; a component allowed by --allow-gpl drops
# its items from the patterns (and only those).
name_items=(x265=x265 x264=x264 poppler exiv2 imagequant jbig ghostscript=ghostscript
  'libgs\.so' ffmpeg-gpl=ffmpeg postproc xvidcore fftw djvulibre autotrace
  'demosaic[-_]pack[-_]gpl' 'dng[-_]?sdk=dng-sdk' 'emf2svg=libemf2svg')

# Strings that only exist inside binaries built from or linked against those components.
# Ghostscript is only ever allowed as the separate `gs` program (--ghostscript): a
# library linking libgs still fails, allowlist or not. The Adobe DNG SDK is compiled
# into libraw_r with hidden visibility, so it is recognised by the RTTI names of its
# classes (dng_negative, dng_host), which a LibRaw built without it does not contain.
content_items=('libx265\.so=x265' x265_encoder_open=x265 'libx264\.so=x264'
  x264_encoder_open=x264 libpoppler poppler_document_new libexiv2 'Exiv2::'
  'libimagequant\.so' libjbig jbg_enc_init 'libgs\.so' gsapi_new_instance
  --enable-gpl=ffmpeg --enable-nonfree --enable-libx264=x264 --enable-libx265=x265
  'libpostproc\.so' 'libxvidcore\.so' libfftw3 fftw_plan_dft libdjvulibre
  ddjvu_document_create libautotrace at_splines_new '12dng_negative=dng-sdk'
  '8dng_host=dng-sdk' 'libemf2svg\.so=libemf2svg' 'emf2svg_is_emfplus=libemf2svg')

# Extra strings refused in a versions file (besides the component names).
versions_items=(enable-gpl=ffmpeg enable-nonfree)

# FFmpeg configure switches refused in `ffmpeg -buildconf` (as --enable-<item>).
buildconf_items=(gpl=ffmpeg nonfree libx264=x264 libx265=x265 libxvid frei0r librubberband
  libvidstab libzvbi libcdio libdavs2 'libxavs2?')

# The GPL components --allow-gpl can allow, and the licences it accepts for them.
# Ghostscript (WP-99, ADR 0011) is the single AGPL component, and only as exactly
# AGPL-3.0-or-later; no other component may be AGPL. dng-sdk (WP-103) is the Adobe DNG
# SDK, allowed only in the gpl flavor and only as exactly LicenseRef-Adobe-DNG-SDK.
gpl_allowable_components=' x264 x265 ffmpeg ghostscript dng-sdk libemf2svg '
gpl_allowable_licenses=' GPL-2.0-only GPL-2.0-or-later GPL-3.0-only GPL-3.0-or-later '
agpl_component=ghostscript
agpl_license=AGPL-3.0-or-later
dngsdk_component=dng-sdk
dngsdk_license=LicenseRef-Adobe-DNG-SDK

# Identifiers an XI_COMPONENT_LICENSES expression may use (--licenses). GPL-* ids are
# accepted there too (--licenses only runs in gpl mode); AGPL, SSPL and anything not
# listed fail.
# LicenseRef-Adobe-DNG-SDK (the Adobe DNG SDK License Agreement: no SPDX identifier,
# permissive with an indemnity clause; ADR 0006 amendment WP-103) is not listed: it is
# accepted only for an allowed dng-sdk, like the AGPL for ghostscript.
known_license_ids=' MIT MIT-Modern-Variant BSD-2-Clause BSD-3-Clause Apache-2.0 ISC Zlib
  libpng-2.0 IJG libtiff FTL HPND-sell-variant Bitstream-Vera OFL-1.1 ImageMagick MPL-1.1
  MPL-2.0 CDDL-1.0 LGPL-2.0-or-later LGPL-2.1-only LGPL-2.1-or-later LGPL-3.0-only
  LGPL-3.0-or-later PCRE2-exception GPL-2.0-only GPL-2.0-or-later GPL-3.0-only
  GPL-3.0-or-later '
known_license_ids=${known_license_ids//$'\n'/ }

failures=0

report() {
  echo "check-licenses: FORBIDDEN $1" >&2
  failures=$((failures + 1))
}

is_elf() {
  [ -f "$1" ] && [ "$(LC_ALL=C head -c 4 "$1" 2>/dev/null)" = $'\177ELF' ]
}

# 0. GPL allowlist (gpl flavor).
allowed_gpl=' '
allowed_gpl_entries=
component_licenses=
if [ -n "$gpl_file" ]; then
  if [ ! -f "$gpl_file" ]; then
    echo "check-licenses: $gpl_file not found" >&2
    exit 2
  fi
  # `source name` would search PATH first.
  case "$gpl_file" in */*) ;; *) gpl_file=./$gpl_file ;; esac
  # shellcheck disable=SC1090
  gpl_list="$(source "$gpl_file" && echo "${XI_GPL_COMPONENTS:-}")"
  # shellcheck disable=SC1090
  component_licenses="$(source "$gpl_file" && echo "${XI_COMPONENT_LICENSES:-}")"
  if [ -z "$gpl_list" ]; then
    report "--allow-gpl $gpl_file lists no XI_GPL_COMPONENTS"
  fi
  for entry in $gpl_list; do
    component=${entry%%=*} license=${entry#*=}
    if [ "$component" = "$entry" ] || [ -z "$license" ]; then
      report "GPL allowlist entry '$entry' in $gpl_file is not component=SPDX"
    elif [[ $gpl_allowable_components != *" $component "* ]]; then
      report "GPL allowlist entry '$entry' in $gpl_file: only x264, x265, ffmpeg, ghostscript, dng-sdk and libemf2svg can be allowed"
    elif [ "$component" = "$agpl_component" ] && [ "$license" != "$agpl_license" ]; then
      report "GPL allowlist entry '$entry' in $gpl_file: ghostscript is allowed as exactly $agpl_license"
    elif [ "$component" = "$dngsdk_component" ] && [ "$license" != "$dngsdk_license" ]; then
      report "GPL allowlist entry '$entry' in $gpl_file: dng-sdk is allowed as exactly $dngsdk_license"
    elif [ "$component" != "$agpl_component" ] && [ "$component" != "$dngsdk_component" ] &&
      [[ $gpl_allowable_licenses != *" $license "* ]]; then
      report "GPL allowlist entry '$entry' in $gpl_file: licence $license is not GPL-2.0/GPL-3.0"
    else
      allowed_gpl+="$component "
      allowed_gpl_entries+=" $entry"
    fi
  done
fi

# gpl_license component: the licence --allow-gpl allowed the component under.
gpl_license() {
  local entry
  for entry in $allowed_gpl_entries; do
    [ "${entry%%=*}" = "$1" ] && echo "${entry#*=}"
  done
}

# join_items item...: the items whose component is not allowed, joined with `|`.
join_items() {
  local item regex component out=
  for item in "$@"; do
    regex=${item%=*} component=
    [ "$regex" != "$item" ] && component=${item##*=}
    if [ -n "$component" ] && [[ $allowed_gpl == *" $component "* ]]; then
      continue
    fi
    out+="${out:+|}$regex"
  done
  echo "$out"
}

name_pattern="$(join_items "${name_items[@]}")"
content_pattern="$(join_items "${content_items[@]}")"
versions_pattern="$name_pattern|$(join_items "${versions_items[@]}")"
buildconf_pattern="--enable-($(join_items "${buildconf_items[@]}"))"

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
  done < <(grep -inE -- "$versions_pattern" "$versions_file")
done

# 5. FFmpeg build configuration and licence (full and gpl flavors).
if [ -n "$ffmpeg_bin" ]; then
  if ! buildconf="$("$ffmpeg_bin" -hide_banner -buildconf 2>&1)"; then
    report "$ffmpeg_bin -buildconf failed: $buildconf"
  else
    while IFS= read -r flag; do
      report "ffmpeg configured with $flag"
    done < <(grep -oE -- "$buildconf_pattern" <<< "$buildconf" | sort -u)
  fi
  ffmpeg_license="$("$ffmpeg_bin" -hide_banner -L 2>&1)"
  if [[ $allowed_gpl == *" ffmpeg "* ]]; then
    # gpl flavor: the build must really be the GPL one the notice declares.
    if ! grep -qE -- '--enable-gpl( |$)' <<< "${buildconf:-}"; then
      report "ffmpeg is allowed as $(gpl_license ffmpeg) but $ffmpeg_bin was not configured with --enable-gpl"
    fi
    if [[ $(gpl_license ffmpeg) == GPL-2.0* ]] && grep -qE -- '--enable-version3( |$)' <<< "${buildconf:-}"; then
      report "ffmpeg is allowed as $(gpl_license ffmpeg) but configured with --enable-version3 (GPL-3.0)"
    fi
    if ! grep -q 'GNU General Public License' <<< "$ffmpeg_license" || grep -q 'Lesser' <<< "$ffmpeg_license"; then
      report "$ffmpeg_bin -L does not report the GNU General Public License"
    fi
  elif ! grep -q 'GNU Lesser General Public' <<< "$ffmpeg_license"; then
    report "$ffmpeg_bin -L does not report the GNU Lesser General Public License"
  fi
fi

# 5b. Ghostscript (gpl flavor, WP-99): allowed only through --allow-gpl, as the one
# `gs` program with exactly the devices the server uses.
if [ -n "$gs_bin" ]; then
  if [[ $allowed_gpl != *" $agpl_component "* ]]; then
    report "$gs_bin: Ghostscript ($agpl_license) is only allowed in the gpl gate"
  elif ! gs_version="$("$gs_bin" --version 2>&1)"; then
    report "$gs_bin --version failed: $gs_version"
  else
    gs_devices="$("$gs_bin" -h 2>&1 | sed -n '/^Available devices:/,/^Search path:/p' | sed '1d;$d' | xargs)"
    if [ "$gs_devices" != "bbox nullpage png16m pngalpha" ]; then
      report "$gs_bin has devices beyond bbox, png16m and pngalpha: $gs_devices"
    fi
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
  [ -n "$gs_bin" ] && elf_files+=("$gs_bin")
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

# 8. Licence of every component in the bundle (gpl flavor).
if [ -n "$licenses_dir" ]; then
  if [ ! -d "$licenses_dir" ]; then
    echo "check-licenses: $licenses_dir is not a directory" >&2
    exit 2
  fi
  listed=' '
  while read -r component expression; do
    [ -n "$component" ] || continue
    listed+="$component "
    if [ -z "$expression" ]; then
      report "component $component has no licence in XI_COMPONENT_LICENSES"
      continue
    fi
    for id in $expression; do
      id=${id//[()]/}
      case "$id" in
        AND | OR | WITH | '') ;;
        "$agpl_license")
          # Only the allowed Ghostscript entry may carry the AGPL.
          if [ "$component" != "$agpl_component" ] || [[ $allowed_gpl != *" $agpl_component "* ]]; then
            report "component $component: licence $id is not allowed (AGPL only for an allowed ghostscript)"
          fi
          ;;
        "$dngsdk_license")
          # Only the allowed dng-sdk entry may carry the Adobe DNG SDK licence.
          if [ "$component" != "$dngsdk_component" ] || [[ $allowed_gpl != *" $dngsdk_component "* ]]; then
            report "component $component: licence $id is not allowed (only for an allowed dng-sdk)"
          fi
          ;;
        *) [[ $known_license_ids == *" $id "* ]] || report "component $component: licence $id is not allowed (AGPL, SSPL and unknown licences are refused)" ;;
      esac
    done
  done <<< "$component_licenses"
  components=0
  while IFS= read -r entry; do
    entry=${entry##*/}
    case "$entry" in SOURCES.md | LICENSE-GPL.md) continue ;; esac
    components=$((components + 1))
    if [[ $listed != *" $entry "* ]]; then
      report "licence bundle entry $entry has no licence in XI_COMPONENT_LICENSES (unknown licence)"
    fi
  done < <(find "$licenses_dir" -mindepth 1 -maxdepth 1 | sort)
  notice="$licenses_dir/LICENSE-GPL.md"
  if [ ! -s "$notice" ]; then
    report "$notice is missing"
  elif ! grep -q 'GPL-2.0-or-later' "$notice"; then
    report "$notice does not declare GPL-2.0-or-later"
  elif [[ $allowed_gpl == *" $agpl_component "* ]] && ! grep -q "$agpl_license" "$notice"; then
    report "$notice does not declare Ghostscript's $agpl_license"
  fi
  echo "check-licenses: $components component(s) in $licenses_dir with known licences"
fi

if [ "$failures" -gt 0 ]; then
  echo "check-licenses: $failures forbidden component(s) found; see docs/adr/0006-license-policy.md" >&2
  exit 1
fi

extras=""
if [ -n "$ffmpeg_bin" ]; then
  if [[ $allowed_gpl == *" ffmpeg "* ]]; then extras+=", ffmpeg GPL"; else extras+=", ffmpeg LGPL"; fi
fi
[ -n "$gs_bin" ] && extras+=", ghostscript ${gs_version:-} (AGPL, devices: ${gs_devices:-})"
[ "$use_ldd" = "1" ] && extras+=", ldd"
[ -n "$models_dir" ] && extras+=", models"
[ -n "$licenses_dir" ] && extras+=", licence bundle"
[ -n "$gpl_file" ] && extras+=", GPL allowed:${allowed_gpl% }"
echo "check-licenses: OK ($lib_dir${versions_files[*]+, ${versions_files[*]}}$extras)"
