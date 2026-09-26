package transform

import (
	"slices"
	"strings"
)

// Detection classes (WP-72/73). Face and object gravity, crop_objects and
// the detection filters name what to look for with class names: "face"
// (the face model) or one of the 80 COCO classes the object model (YOLOX)
// predicts. The vocabulary lives in the model so that Validate rejects an
// unknown class before anything is fetched, and so that the planner and
// internal/detect agree on the names.

// FaceClass is the class of every face detection.
const FaceClass = "face"

// NumObjectClasses is the number of COCO classes of the object model.
const NumObjectClasses = 80

// ObjectClasses returns the 80 COCO class names in the object model's
// output order (yolox/data/datasets/coco_classes.py), with spaces as in
// the source ("traffic light", "cell phone"). The index is the class id of
// the /info JSON. It returns a fresh slice: there are no package-level
// tables.
func ObjectClasses() []string {
	return []string{
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

// ObjectClassID returns the COCO index of a canonical object class name,
// or -1.
func ObjectClassID(name string) int {
	for i, c := range ObjectClasses() {
		if c == name {
			return i
		}
	}
	return -1
}

// CanonicalClass normalises a class name from a URL: lower case, '_', '-'
// and '+' read as spaces, runs of spaces collapsed ("Traffic_Light" →
// "traffic light").
func CanonicalClass(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '_', '-', '+':
			return ' '
		}
		return r
	}, strings.ToLower(s))
	return strings.Join(strings.Fields(s), " ")
}

// ResolveClass maps a class name from a URL to the canonical class names
// it stands for: "face" (or "faces"), a COCO class, or a COCO
// supercategory ("vehicle", "animal", "food", ... as in the COCO 2017
// annotations; "person" is both). A plural of a class ("cars", "dogs",
// "people") names the class. ok is false for anything else.
func ResolveClass(s string) (classes []string, ok bool) {
	c := CanonicalClass(s)
	switch c {
	case "":
		return nil, false
	case FaceClass, "faces":
		return []string{FaceClass}, true
	case "people", "persons":
		return []string{"person"}, true
	}
	if ObjectClassID(c) >= 0 {
		return []string{c}, true
	}
	if members := supercategory(c); members != nil {
		return members, true
	}
	if base, found := strings.CutSuffix(c, "s"); found && ObjectClassID(base) >= 0 {
		return []string{base}, true
	}
	if base, found := strings.CutSuffix(c, "es"); found && ObjectClassID(base) >= 0 {
		return []string{base}, true
	}
	return nil, false
}

// supercategory returns the COCO classes of a COCO supercategory, nil for
// any other name.
func supercategory(name string) []string {
	switch name {
	case "vehicle", "vehicles":
		return []string{"bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat"}
	case "outdoor":
		return []string{"traffic light", "fire hydrant", "stop sign", "parking meter", "bench"}
	case "animal", "animals":
		return []string{"bird", "cat", "dog", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe"}
	case "accessory", "accessories":
		return []string{"backpack", "umbrella", "handbag", "tie", "suitcase"}
	case "sports":
		return []string{
			"frisbee", "skis", "snowboard", "sports ball", "kite", "baseball bat", "baseball glove",
			"skateboard", "surfboard", "tennis racket",
		}
	case "kitchen":
		return []string{"bottle", "wine glass", "cup", "fork", "knife", "spoon", "bowl"}
	case "food":
		return []string{"banana", "apple", "sandwich", "orange", "broccoli", "carrot", "hot dog", "pizza", "donut", "cake"}
	case "furniture":
		return []string{"chair", "couch", "potted plant", "bed", "dining table", "toilet"}
	case "electronic", "electronics":
		return []string{"tv", "laptop", "mouse", "remote", "keyboard", "cell phone"}
	case "appliance", "appliances":
		return []string{"microwave", "oven", "toaster", "sink", "refrigerator"}
	case "indoor":
		return []string{"book", "clock", "vase", "scissors", "teddy bear", "hair drier", "toothbrush"}
	}
	return nil
}

// ResolveClasses resolves every name of a class list (ResolveClass) into
// one list without duplicates, in first-seen order. An empty list is nil:
// every class. ok is false when a name is unknown.
func ResolveClasses(names []string) (classes []string, ok bool) {
	for _, n := range names {
		cs, ok := ResolveClass(n)
		if !ok {
			return nil, false
		}
		for _, c := range cs {
			if !slices.Contains(classes, c) {
				classes = append(classes, c)
			}
		}
	}
	return classes, true
}
