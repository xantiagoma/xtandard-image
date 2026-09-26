package detect

// FaceClass is the class name of every face detection.
const FaceClass = "face"

// cocoClassCount is the number of COCO classes YOLOX-s predicts.
const cocoClassCount = 80

// cocoClasses returns the 80 COCO class names in YOLOX's output order
// (yolox/data/datasets/coco_classes.py), with spaces as in the source
// ("traffic light", "cell phone"). It returns a fresh array: there are no
// package-level tables (go-standards: no globals).
func cocoClasses() [cocoClassCount]string {
	return [cocoClassCount]string{
		"person", "bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat",
		"traffic light", "fire hydrant", "stop sign", "parking meter", "bench", "bird", "cat",
		"dog", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe", "backpack",
		"umbrella", "handbag", "tie", "suitcase", "frisbee", "skis", "snowboard", "sports ball",
		"kite", "baseball bat", "baseball glove", "skateboard", "surfboard", "tennis racket",
		"bottle", "wine glass", "cup", "fork", "knife", "spoon", "bowl", "banana", "apple",
		"sandwich", "orange", "broccoli", "carrot", "hot dog", "pizza", "donut", "cake", "chair",
		"couch", "potted plant", "bed", "dining table", "toilet", "tv", "laptop", "mouse",
		"remote", "keyboard", "cell phone", "microwave", "oven", "toaster", "sink",
		"refrigerator", "book", "clock", "vase", "scissors", "teddy bear", "hair drier",
		"toothbrush",
	}
}

// ObjectClasses returns the class names Objects accepts and reports, in the
// object model's order (the index is the class id of the /info JSON).
func ObjectClasses() []string {
	c := cocoClasses()
	return c[:]
}

// ClassID returns the id of a class name for the /info JSON: 0 for faces
// (the face model's only class), the COCO index for objects, and -1 for an
// unknown name.
func ClassID(name string) int {
	if name == FaceClass {
		return 0
	}
	for i, c := range cocoClasses() {
		if c == name {
			return i
		}
	}
	return -1
}
