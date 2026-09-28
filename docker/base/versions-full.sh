# shellcheck shell=bash
# Pinned versions and SHA256 checksums of the components that only the `full`
# flavor adds on top of slim (WP-70): FFmpeg (LGPL, video only), libvpx, ONNX Runtime
# (prebuilt, CPU only) and the detection and classification models.
#
# Kept apart from versions.sh so that pinning or bumping a full-only component never
# invalidates the slim build (the slim stages COPY versions.sh, not this file).
# Bump a version, then run ./update-checksums.sh to refresh the *_SHA256 lines.
# Forbidden components are listed in check-licenses.sh (ADR 0006); never add them here.

# Revision of the full flavor. Its tag is <VIPS_VERSION>-r<XI_BASE_FULL_REVISION>-full.
# Full r6 = slim r5 (versions.sh XI_BASE_REVISION) + the components below.
export XI_BASE_FULL_REVISION=6

# --- Video (FFmpeg under LGPL-2.1+, GPL and nonfree parts disabled) ---------------
# AV1 is decoded by dav1d and encoded by libaom; both are already built for slim
# (versions.sh DAV1D_VERSION, AOM_VERSION) and FFmpeg links those same libraries.
export LIBVPX_VERSION=1.17.0
export LIBVPX_SHA256=1020f184046187baa2985dbde38e0691f49c44088bca7a1842b0236c6081dc0a
export FFMPEG_VERSION=8.1.3
export FFMPEG_SHA256=7138d28c96d9d3e3af4ee3d8cad72741f8ffb40da90c1112235dea3ecd3178a3

# --- ONNX Runtime (MIT), official prebuilt release, CPU execution provider ---------
export ONNXRUNTIME_VERSION=1.30.0
export ONNXRUNTIME_SHA256_AMD64=a5ed5a3cac51fbb2e90da632ae43d19212faaa20e76484e62bcb7c23ddb3b3fd
export ONNXRUNTIME_SHA256_ARM64=e16a27a8ed330bbc698df7330b0cf56e722f354e3bcc92118682c74ef3c3e3da

# --- Detection models (ADR 0006: permissive licences only) --------------------------
# YuNet face detector, MIT, from opencv_zoo at a pinned commit (Git LFS object, fetched
# through the media URL). The model and its LICENSE come from the same commit.
export YUNET_VERSION=2023mar
export YUNET_COMMIT=47534e27c9851bb1128ccc0102f1145e27f23f98
export YUNET_SHA256=8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4
export YUNET_LICENSE_SHA256=c83b8120c50ccbd4c4f96edf53141bdd566ebb8f8e9227e415326aa1b1aba958
# YOLOX-s object detector (COCO, 80 classes), Apache-2.0, Megvii YOLOX release asset.
export YOLOX_VERSION=0.1.1rc0
export YOLOX_SHA256=c5c2d13e59ae883e6af3b45daea64af4833a4951c92d116ec270d9ddbe998063
export YOLOX_LICENSE_SHA256=577c03d505ec80f667ebf96ebd0cc4f6825c817ca1088ee348c48aaabd51bd92
# ImageNet classifier (WP-110, info option classify): timm mobilenetv3_large_100
# (ra_in1k weights, 1000 classes, 75.8% ImageNet top-1) exported to ONNX (opset 17) by
# the ONNX Model Zoo, Apache-2.0 (the zoo's LICENSE; timm is Apache-2.0 too). A Git LFS
# object at a pinned onnx/models commit, fetched through the media URL; the LICENSE is
# the same commit's.
export CLASSIFIER_COMMIT=4f43949841cb55a0b98dc8fcd045431ccafd9f96
export CLASSIFIER_SHA256=b0b785f44a068dee4ff35bd88b114d4fd17a8bd635816f8f0ee46fa8b196c29f
export CLASSIFIER_LICENSE_SHA256=cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30
