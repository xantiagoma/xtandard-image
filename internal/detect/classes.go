package detect

import "github.com/xantiagoma/xtandard-image/internal/transform"

// FaceClass is the class name of every face detection.
const FaceClass = transform.FaceClass

// cocoClassCount is the number of COCO classes YOLOX-s predicts.
const cocoClassCount = transform.NumObjectClasses

// cocoClasses returns the 80 COCO class names in YOLOX's output order
// (transform.ObjectClasses: the vocabulary is part of the request model,
// so Validate rejects unknown classes before anything is fetched).
func cocoClasses() []string { return transform.ObjectClasses() }

// ObjectClasses returns the class names Objects accepts and reports, in the
// object model's order (the index is the class id of the /info JSON).
func ObjectClasses() []string { return transform.ObjectClasses() }

// ClassID returns the id of a class name for the /info JSON: 0 for faces
// (the face model's only class), the COCO index for objects, and -1 for an
// unknown name.
func ClassID(name string) int {
	if name == FaceClass {
		return 0
	}
	return transform.ObjectClassID(name)
}
