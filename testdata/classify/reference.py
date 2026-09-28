# Reference classifications (WP-110): timm's evaluation transform for mobilenetv3_large_100
# (resize the short side to 224 / 0.875 = 256 with bicubic interpolation, centre-crop 224,
# ImageNet mean/std, RGB, NCHW) reimplemented with Pillow and NumPy, inference with
# onnxruntime, softmax, top 5. Used to derive testdata expectations independently of the
# Go implementation.
#
#   python reference.py <mobilenetv3_large_100.onnx> <labels.txt> <photo.jpg>...
#
# prints {"<photo base name>": {"top5": [[class_id, name, probability], ...]}} as JSON.
import json
import os
import sys

import numpy as np
import onnxruntime
from PIL import Image

MEAN = np.array([0.485, 0.456, 0.406], np.float32)
STD = np.array([0.229, 0.224, 0.225], np.float32)
SIZE, CROP_PCT = 224, 0.875


def preprocess(img):
    # timm transforms_factory.transforms_imagenet_eval: Resize(int(224 / 0.875)) on the
    # short side, CenterCrop(224), ToTensor, Normalize.
    scale = int(SIZE / CROP_PCT)
    w, h = img.size
    if w <= h:
        nw, nh = scale, int(scale * h / w)
    else:
        nw, nh = int(scale * w / h), scale
    img = img.resize((nw, nh), Image.BICUBIC)
    left, top = int(round((nw - SIZE) / 2.0)), int(round((nh - SIZE) / 2.0))
    img = img.crop((left, top, left + SIZE, top + SIZE))
    x = np.asarray(img, np.float32) / 255.0
    x = (x - MEAN) / STD
    return x.transpose(2, 0, 1)[None]


def softmax(v):
    e = np.exp(v - v.max())
    return e / e.sum()


def main():
    model, labels_file, photos = sys.argv[1], sys.argv[2], sys.argv[3:]
    labels = open(labels_file).read().splitlines()
    so = onnxruntime.SessionOptions()
    so.intra_op_num_threads = 1
    s = onnxruntime.InferenceSession(model, so, providers=["CPUExecutionProvider"])
    name = s.get_inputs()[0].name
    out = {}
    for p in photos:
        prob = softmax(s.run(None, {name: preprocess(Image.open(p).convert("RGB"))})[0][0])
        top = np.argsort(-prob)[:5]
        out[os.path.basename(p)] = {"top5": [[int(i), labels[i], round(float(prob[i]), 4)] for i in top]}
    print(json.dumps(out, indent=1))


if __name__ == "__main__":
    main()
