# Detection expectations

- `expected.json`: faces (box, score, 5 landmarks) and objects (class, box, score ≥ 0.5) that the
  reference implementations find on `../fixtures/detect/*.jpg`, in source pixels
  (`[x1, y1, x2, y2]`).
- `reference.py`: the script that computes them. YuNet goes through OpenCV's
  `cv2.FaceDetectorYN`; YOLOX-s goes through onnxruntime with the pre- and post-processing
  functions copied from the official YOLOX demo (`demo/ONNXRuntime/onnx_inference.py`,
  `yolox/data/data_augment.py`, `yolox/utils/demo_utils.py`; Apache-2.0, see `NOTICE`).

`internal/detect` tests (`TestFixtures`, `TestScaled`, `TestMirrored`) require every expected box
to be matched by a detection of the same class with IoU ≥ 0.5. Sources, licences and checksums
of the photos: `../LICENSES.md` ("Detection photos"). How to regenerate: `../README.md`
("Detection expectations").
