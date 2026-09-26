# Reference detections: YOLOX demo/ONNXRuntime/onnx_inference.py pre/post-processing
# (copied from yolox/data/data_augment.py preproc and yolox/utils/demo_utils.py) and
# OpenCV's cv2.FaceDetectorYN (YuNet). Used to derive testdata expectations independently
# of the Go implementation.
import json, sys
import cv2
import numpy as np
import onnxruntime

COCO = ("person","bicycle","car","motorcycle","airplane","bus","train","truck","boat","traffic light","fire hydrant","stop sign","parking meter","bench","bird","cat","dog","horse","sheep","cow","elephant","bear","zebra","giraffe","backpack","umbrella","handbag","tie","suitcase","frisbee","skis","snowboard","sports ball","kite","baseball bat","baseball glove","skateboard","surfboard","tennis racket","bottle","wine glass","cup","fork","knife","spoon","bowl","banana","apple","sandwich","orange","broccoli","carrot","hot dog","pizza","donut","cake","chair","couch","potted plant","bed","dining table","toilet","tv","laptop","mouse","remote","keyboard","cell phone","microwave","oven","toaster","sink","refrigerator","book","clock","vase","scissors","teddy bear","hair drier","toothbrush")

def preproc(img, input_size, swap=(2, 0, 1)):
    padded_img = np.ones((input_size[0], input_size[1], 3), dtype=np.uint8) * 114
    r = min(input_size[0] / img.shape[0], input_size[1] / img.shape[1])
    resized_img = cv2.resize(img, (int(img.shape[1] * r), int(img.shape[0] * r)), interpolation=cv2.INTER_LINEAR).astype(np.uint8)
    padded_img[: int(img.shape[0] * r), : int(img.shape[1] * r)] = resized_img
    padded_img = padded_img.transpose(swap)
    return np.ascontiguousarray(padded_img, dtype=np.float32), r

def nms(boxes, scores, nms_thr):
    x1, y1, x2, y2 = boxes[:, 0], boxes[:, 1], boxes[:, 2], boxes[:, 3]
    areas = (x2 - x1 + 1) * (y2 - y1 + 1)
    order = scores.argsort()[::-1]
    keep = []
    while order.size > 0:
        i = order[0]; keep.append(i)
        xx1 = np.maximum(x1[i], x1[order[1:]]); yy1 = np.maximum(y1[i], y1[order[1:]])
        xx2 = np.minimum(x2[i], x2[order[1:]]); yy2 = np.minimum(y2[i], y2[order[1:]])
        w = np.maximum(0.0, xx2 - xx1 + 1); h = np.maximum(0.0, yy2 - yy1 + 1)
        inter = w * h
        ovr = inter / (areas[i] + areas[order[1:]] - inter)
        order = order[np.where(ovr <= nms_thr)[0] + 1]
    return keep

def class_aware(boxes, scores, nms_thr, score_thr):
    out = []
    for c in range(scores.shape[1]):
        s = scores[:, c]; m = s > score_thr
        if m.sum() == 0: continue
        vb, vs = boxes[m], s[m]
        for k in nms(vb, vs, nms_thr):
            out.append((vb[k].tolist(), float(vs[k]), COCO[c]))
    return out

def demo_postprocess(outputs, img_size):
    grids, strides = [], []
    for stride in [8, 16, 32]:
        hs, ws = img_size[0] // stride, img_size[1] // stride
        xv, yv = np.meshgrid(np.arange(ws), np.arange(hs))
        grid = np.stack((xv, yv), 2).reshape(1, -1, 2)
        grids.append(grid); strides.append(np.full((*grid.shape[:2], 1), stride))
    grids = np.concatenate(grids, 1); strides = np.concatenate(strides, 1)
    outputs[..., :2] = (outputs[..., :2] + grids) * strides
    outputs[..., 2:4] = np.exp(outputs[..., 2:4]) * strides
    return outputs

def yolox(path, model):
    img = cv2.imread(path)
    x, r = preproc(img, (640, 640))
    s = onnxruntime.InferenceSession(model)
    out = s.run(None, {s.get_inputs()[0].name: x[None]})
    p = demo_postprocess(out[0], (640, 640))[0]
    b = p[:, :4]; sc = p[:, 4:5] * p[:, 5:]
    xyxy = np.ones_like(b)
    xyxy[:, 0] = b[:, 0] - b[:, 2] / 2; xyxy[:, 1] = b[:, 1] - b[:, 3] / 2
    xyxy[:, 2] = b[:, 0] + b[:, 2] / 2; xyxy[:, 3] = b[:, 1] + b[:, 3] / 2
    xyxy /= r
    dets = class_aware(xyxy, sc, 0.45, 0.3)
    return [{"class": c, "score": round(s, 3), "box": [round(v) for v in bb]} for bb, s, c in dets]

def yunet(path, model):
    img = cv2.imread(path)
    h, w = img.shape[:2]
    d = cv2.FaceDetectorYN.create(model, "", (w, h), 0.6, 0.3, 5000)
    _, faces = d.detect(img)
    res = []
    for f in (faces if faces is not None else []):
        x, y, fw, fh = f[:4]
        res.append({"class": "face", "score": round(float(f[14]), 3), "box": [round(x), round(y), round(x + fw), round(y + fh)],
                    "landmarks": [[round(f[4 + 2 * i]), round(f[5 + 2 * i])] for i in range(5)]})
    return res

out = {}
for p in sys.argv[3:]:
    out[p.rsplit("/", 1)[-1]] = {"faces": yunet(p, sys.argv[1]), "objects": yolox(p, sys.argv[2])}
print(json.dumps(out, indent=1))
