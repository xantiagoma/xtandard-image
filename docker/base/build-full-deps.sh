#!/bin/bash
# Builds the `full` flavor additions (WP-70) on top of the slim /opt/xtandard:
#   - libvpx (BSD-3-Clause): VP8/VP9 encoders for FFmpeg
#   - libopus (BSD-3-Clause) and LAME (LGPL-2.0+): Opus and MP3 encoders for FFmpeg
#     (WP-120, video transformations, ADR 0012)
#   - FFmpeg (LGPL-2.1+): shared libavcodec/avformat/avutil/swscale/swresample/avfilter
#     plus the ffmpeg/ffprobe CLIs, --disable-gpl --disable-nonfree, linking the slim
#     dav1d (AV1 decode), libaom (AV1 encode) and FreeType/HarfBuzz/FriBidi/Fontconfig
#     (drawtext)
#   - ONNX Runtime (MIT): the official prebuilt CPU library in /usr/local/lib
#   - detection models (YuNet MIT, YOLOX-s Apache-2.0) and the ImageNet classifier
#     (MobileNetV3-Large, Apache-2.0) in /usr/share/xtandard/models
# then extends the licence bundle and SOURCES.md and runs the licence gate.
#
# Runs in the `full-deps` stage after build-deps.sh (BASH_ENV=bashrc.sh sets the flags).

set -euo pipefail

my_dir="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=versions.sh
source "$my_dir/versions.sh"
# shellcheck source=versions-full.sh
source "$my_dir/versions-full.sh"

FULL_DEPS_SRC=${FULL_DEPS_SRC:-/root/full-deps}
TARGET_PATH=${XI_PREFIX:-/opt/xtandard}
ORT_PREFIX=${ORT_PREFIX:-/usr/local}
MODELS_DIR=${MODELS_DIR:-/usr/share/xtandard/models}
LICENSES="$TARGET_PATH/share/licenses"

print_build_stage() {
  echo ""
  echo "== Build $1 $2 ====================================="
  echo ""
}

# add_sbom name version cpe: CycloneDX stub, same format as build-deps.sh.
add_sbom() {
  local name=$1 version=$2 cpe=$3
  local filename="$TARGET_PATH/share/sbom/$name.cdx.json"
  mkdir -p "$(dirname "$filename")"
  cat << EOF > "$filename"
{
  "\$schema": "https://cyclonedx.org/schema/bom-1.7.schema.json",
  "bomFormat": "CycloneDX",
  "specVersion": "1.7",
  "version": 1,
  "metadata": {
    "timestamp": "$(date -u +"%Y-%m-%dT%H:%M:%SZ")",
    "component": {
      "type": "library",
      "name": "$name",
      "version": "$version",
      "cpe": "$cpe",
      "properties": [
        { "name": "syft:package:type", "value": "binary" }
      ]
    }
  }
}
EOF
}

print_build_stage libvpx "$LIBVPX_VERSION"
cd "$FULL_DEPS_SRC/libvpx"
vpx_args=()
case "$(uname -m)" in
  x86_64) vpx_args+=(--as=nasm) ;;
esac
CFLAGS="${CFLAGS} -O3" CXXFLAGS="${CXXFLAGS} -O3" ./configure \
  --prefix="$TARGET_PATH" \
  --libdir="$TARGET_PATH/lib" \
  --enable-shared \
  --disable-static \
  --enable-pic \
  --enable-vp8 \
  --enable-vp9 \
  --enable-runtime-cpu-detect \
  --disable-examples \
  --disable-tools \
  --disable-docs \
  --disable-unit-tests \
  --disable-decode-perf-tests \
  --disable-encode-perf-tests \
  "${vpx_args[@]}"
make -j"$(nproc)"
make install
add_sbom libvpx "$LIBVPX_VERSION" "cpe:2.3:a:webmproject:libvpx:$LIBVPX_VERSION:*:*:*:*:*:*:*"

print_build_stage opus "$OPUS_VERSION"
cd "$FULL_DEPS_SRC/opus"
# Library only. The deep-learning extensions (DRED, deep PLC, OSCE) stay off, as by
# default: they would pull in model weights for decoder-side features the server
# does not use.
CFLAGS="${CFLAGS} -O3" ./configure \
  --prefix="$TARGET_PATH" \
  --libdir="$TARGET_PATH/lib" \
  --enable-shared \
  --disable-static \
  --disable-doc \
  --disable-extra-programs
make -j"$(nproc)"
make install
add_sbom opus "$OPUS_VERSION" "cpe:2.3:a:opus-codec:opus:$OPUS_VERSION:*:*:*:*:*:*:*"

print_build_stage lame "$LAME_VERSION"
cd "$FULL_DEPS_SRC/lame"
# libmp3lame only: no frontend, no mpglib decoder (FFmpeg decodes MP3 itself), no
# GTK frame analyzer. The export list names lame_init_old, which 3.100 no longer
# defines (the fix every distribution carries).
sed -i '/^lame_init_old$/d' include/libmp3lame.sym
lame_args=()
case "$(uname -m)" in
  x86_64) lame_args+=(--enable-nasm) ;;
esac
CFLAGS="${CFLAGS} -O3" ./configure \
  --prefix="$TARGET_PATH" \
  --libdir="$TARGET_PATH/lib" \
  --enable-shared \
  --disable-static \
  --disable-frontend \
  --disable-decoder \
  --disable-gtktest \
  --disable-analyzer-hooks \
  "${lame_args[@]}"
make -j"$(nproc)"
make install
add_sbom lame "$LAME_VERSION" "cpe:2.3:a:lame_project:lame:$LAME_VERSION:*:*:*:*:*:*:*"

print_build_stage ffmpeg "$FFMPEG_VERSION"
cd "$FULL_DEPS_SRC/ffmpeg"
# Everything off, then exactly what WP-74/75/76 and the video transformations of M5
# (WP-120, ADR 0012) need:
#   video: demux mp4/mov, webm/mkv, avi, gif, MPEG-TS (+ ivf, raw video); decode
#     h264/hevc/vp8/vp9/av1 (dav1d) and a few intra codecs found in AVI/MOV; encode
#     VP8/VP9 (libvpx) and AV1 (libaom); raw frames and PNG for thumbnails
#   audio: demux mp3, aac (ADTS), ac3/eac3, flac, ogg, wav; decode aac, mp3, mp2,
#     opus, vorbis, flac, alac, ac3/eac3 and common PCM; encode aac (native), opus
#     (libopus), mp3 (libmp3lame), flac and pcm_s16le; resample with swresample
#   mux mp4/mov/m4a (ipod), webm/mkv, MPEG-TS, HLS, DASH, segment, mp3, ogg/opus,
#     adts, wav, flac
#   filters for trim/resize/crop/pad/fps/rotate/flip/fade, image and text overlays
#     (drawtext: FreeType, HarfBuzz, FriBidi, Fontconfig with the base's fonts) and
#     audio (aresample, volume, loudnorm, amix, fades, waveform image)
# Only the file and pipe protocols (the server reads and writes through custom AVIO);
# no network, no devices, no hardware acceleration, no concat demuxer (it opens paths
# named in its input). LGPL only: --disable-gpl --disable-nonfree and no
# --enable-version3 (no dependency needs it). --disable-autodetect keeps anything else
# on the build host from sneaking in.
./configure \
  --prefix="$TARGET_PATH" \
  --libdir="$TARGET_PATH/lib" \
  --shlibdir="$TARGET_PATH/lib" \
  --incdir="$TARGET_PATH/include" \
  --pkg-config=pkg-config \
  --enable-shared \
  --disable-static \
  --enable-pic \
  --disable-gpl \
  --disable-nonfree \
  --disable-autodetect \
  --enable-pthreads \
  --enable-zlib \
  --disable-doc \
  --disable-htmlpages \
  --disable-manpages \
  --disable-podpages \
  --disable-txtpages \
  --disable-debug \
  --disable-network \
  --disable-avdevice \
  --enable-swresample \
  --disable-ffplay \
  --enable-ffmpeg \
  --enable-ffprobe \
  --disable-everything \
  --enable-libdav1d \
  --enable-libvpx \
  --enable-libaom \
  --enable-libopus \
  --enable-libmp3lame \
  --enable-libfreetype \
  --enable-libharfbuzz \
  --enable-libfribidi \
  --enable-libfontconfig \
  --enable-protocol=file,pipe \
  --enable-demuxer=mov,matroska,avi,gif,ivf,mpegts,rawvideo,image2,image2pipe,mp3,aac,ac3,eac3,flac,ogg,wav \
  --enable-muxer=mp4,mov,ipod,webm,matroska,ivf,mpegts,hls,dash,segment,null,rawvideo,image2,image2pipe,mp3,ogg,opus,adts,wav,flac \
  --enable-decoder=h264,hevc,vp8,vp9,libdav1d,gif,mpeg4,mjpeg,png,rawvideo,aac,aac_latm,mp3,mp3float,mp2,mp2float,opus,vorbis,flac,alac,ac3,eac3,pcm_s16le,pcm_s16be,pcm_s24le,pcm_s24be,pcm_s32le,pcm_f32le,pcm_f64le,pcm_u8,pcm_alaw,pcm_mulaw \
  --enable-encoder=libvpx_vp8,libvpx_vp9,libaom_av1,png,rawvideo,wrapped_avframe,aac,libopus,libmp3lame,flac,pcm_s16le \
  --enable-parser=h264,hevc,vp8,vp9,av1,mpeg4video,mjpeg,png,gif,aac,aac_latm,mpegaudio,opus,vorbis,flac,ac3 \
  --enable-bsf=vp9_superframe,vp9_superframe_split,av1_metadata,extract_extradata,h264_mp4toannexb,hevc_mp4toannexb,aac_adtstoasc \
  --enable-filter=scale,format,null,fps,select,thumbnail,crop,pad,transpose,hflip,vflip,rotate,trim,setpts,settb,setsar,setdar,tile,palettegen,paletteuse,split,overlay,color,fade,colorchannelmixer,drawtext,concat,buffer,buffersink,abuffer,abuffersink,aformat,anull,anullsrc,aresample,atrim,asetpts,asettb,asplit,volume,loudnorm,amix,apad,afade,showwavespic
make -j"$(nproc)"
make install
add_sbom ffmpeg "$FFMPEG_VERSION" "cpe:2.3:a:ffmpeg:ffmpeg:$FFMPEG_VERSION:*:*:*:*:*:*:*"

print_build_stage onnxruntime "$ONNXRUNTIME_VERSION"
# Prebuilt: only the CPU library (libonnxruntime_providers_shared.so is for the
# CUDA/TensorRT-style shared execution providers, which are not shipped) and headers.
ort="$FULL_DEPS_SRC/onnxruntime"
mkdir -p "$ORT_PREFIX/lib/pkgconfig" "$ORT_PREFIX/include/onnxruntime"
cp -a "$ort"/lib/libonnxruntime.so* "$ORT_PREFIX/lib/"
cp -a "$ort"/include/. "$ORT_PREFIX/include/onnxruntime/"
cat > "$ORT_PREFIX/lib/pkgconfig/libonnxruntime.pc" << EOF
prefix=$ORT_PREFIX
libdir=\${prefix}/lib
includedir=\${prefix}/include/onnxruntime

Name: onnxruntime
Description: ONNX Runtime (prebuilt, CPU execution provider)
URL: https://onnxruntime.ai/
Version: $ONNXRUNTIME_VERSION
Libs: -L\${libdir} -lonnxruntime
Cflags: -I\${includedir}
EOF
ldconfig
add_sbom onnxruntime "$ONNXRUNTIME_VERSION" "cpe:2.3:a:microsoft:onnxruntime:$ONNXRUNTIME_VERSION:*:*:*:*:*:*:*"

print_build_stage models "yunet-$YUNET_VERSION yolox_s-$YOLOX_VERSION mobilenetv3_large_100"
mkdir -p "$MODELS_DIR"
cp "$FULL_DEPS_SRC"/models/* "$MODELS_DIR/"
chmod 0644 "$MODELS_DIR"/*
cat > "$MODELS_DIR/MODELS.md" << EOF
# Detection and classification models (xtandard-image full flavor, revision $XI_BASE_FULL_REVISION)

Each \`<name>.onnx\` has its licence text in \`<name>.LICENSE\`. Checksums: \`SHA256SUMS\`.

| File | Model | Licence | Source |
|---|---|---|---|
| face_detection_yunet_${YUNET_VERSION}.onnx | YuNet face detector (libfacedetection) | MIT | https://github.com/opencv/opencv_zoo/tree/${YUNET_COMMIT}/models/face_detection_yunet |
| yolox_s.onnx | YOLOX-s object detector (COCO, 80 classes) | Apache-2.0 | https://github.com/Megvii-BaseDetection/YOLOX/releases/tag/${YOLOX_VERSION} |
| mobilenetv3_large_100.onnx | MobileNetV3-Large ImageNet classifier (timm mobilenetv3_large_100, ra_in1k, 1000 classes) | Apache-2.0 | https://github.com/onnx/models/tree/${CLASSIFIER_COMMIT}/Computer_Vision/mobilenetv3_large_100_Opset17_timm |
EOF
(cd "$MODELS_DIR" && sha256sum ./*.onnx | sed 's| \./| |' > SHA256SUMS && sha256sum -c SHA256SUMS)
mkdir -p "$LICENSES/models"
cp "$MODELS_DIR"/*.LICENSE "$MODELS_DIR/MODELS.md" "$LICENSES/models/"

# --- Cleanup (as in build-deps.sh) ---------------------------------------------
rm -rf "$TARGET_PATH"/lib/*.a "$TARGET_PATH"/lib/*.la
rm -rf "$TARGET_PATH"/share/{doc,man,ffmpeg}
find "$TARGET_PATH/bin" -type f ! -name 'vips*' ! -name ffmpeg ! -name ffprobe -delete
# FFmpeg's `make install` already strips its libraries and programs; libvpx, libopus
# and LAME do not.
find "$TARGET_PATH/lib" -maxdepth 1 -type f \( -name 'libvpx.so.*' -o -name 'libopus.so.*' -o -name 'libmp3lame.so.*' \) \
  -exec strip --strip-unneeded {} +

# --- Licences and SOURCES.md ---------------------------------------------------
for name in libvpx opus lame ffmpeg onnxruntime; do
  dir="$FULL_DEPS_SRC/$name"
  mkdir -p "$LICENSES/$name"
  find "$dir" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'PATENTS*' \
    -o -iname 'AUTHORS*' -o -iname 'ThirdPartyNotices*' \) -exec cp {} "$LICENSES/$name/" \;
done
# FFmpeg ships every licence text it may be built under; this build is LGPL-2.1+ only.
rm -f "$LICENSES"/ffmpeg/COPYING.GPL* "$LICENSES"/ffmpeg/COPYING.LGPLv3
for name in libvpx opus lame ffmpeg onnxruntime; do
  if [ -z "$(ls -A "$LICENSES/$name")" ]; then
    echo "No license file found for $name" >&2
    exit 1
  fi
done
"$TARGET_PATH/bin/ffmpeg" -hide_banner -buildconf > "$LICENSES/ffmpeg/BUILDCONF.txt"
{
  echo ""
  echo "## full flavor (revision ${XI_BASE_FULL_REVISION})"
  echo ""
  echo "Added by docker/base/build-full-deps.sh (versions-full.sh). FFmpeg and its codec"
  echo "libraries (libvpx, libopus, LAME) are dynamically linked (LGPL-2.1+); ONNX Runtime is the official prebuilt"
  echo "library in ${ORT_PREFIX}/lib; the models are in ${MODELS_DIR}."
  echo ""
  echo "| File | Source | SHA256 |"
  echo "|---|---|---|"
  cat "$FULL_DEPS_SRC/SOURCES.rows"
} >> "$LICENSES/SOURCES.md"

# --- Trees for the final images (directory COPY keeps the .so symlinks) -----------
rm -rf /full-extra
mkdir -p "/full-extra/runtime$ORT_PREFIX/lib" "/full-extra/runtime$(dirname "$MODELS_DIR")"
cp -a "$ORT_PREFIX"/lib/libonnxruntime.so* "/full-extra/runtime$ORT_PREFIX/lib/"
cp -a "$MODELS_DIR" "/full-extra/runtime$MODELS_DIR"
cp -a /full-extra/runtime /full-extra/builder
mkdir -p "/full-extra/builder$ORT_PREFIX/lib/pkgconfig" "/full-extra/builder$ORT_PREFIX/include"
cp -a "$ORT_PREFIX/lib/pkgconfig/libonnxruntime.pc" "/full-extra/builder$ORT_PREFIX/lib/pkgconfig/"
cp -a "$ORT_PREFIX/include/onnxruntime" "/full-extra/builder$ORT_PREFIX/include/"

# --- License gate --------------------------------------------------------------
"$my_dir/check-licenses.sh" --ffmpeg "$TARGET_PATH/bin/ffmpeg" --ldd --models "$MODELS_DIR" \
  "$TARGET_PATH/lib" "$my_dir/versions.sh" "$my_dir/versions-full.sh"
"$my_dir/check-licenses.sh" --ldd "$ORT_PREFIX/lib"
