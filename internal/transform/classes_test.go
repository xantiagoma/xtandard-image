package transform

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestResolveClass(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string // nil: unknown
	}{
		{"face", []string{"face"}},
		{"Faces", []string{"face"}},
		{"person", []string{"person"}},
		{"people", []string{"person"}},
		{"traffic_light", []string{"traffic light"}},
		{"Traffic-Light", []string{"traffic light"}},
		{"  cell   phone ", []string{"cell phone"}},
		{"cars", []string{"car"}},
		{"buses", []string{"bus"}},
		{"vehicle", []string{"bicycle", "car", "motorcycle", "airplane", "bus", "train", "truck", "boat"}},
		{"animal", []string{"bird", "cat", "dog", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe"}},
		{"toothbrush", []string{"toothbrush"}},
		{"", nil},
		{"unicorn", nil},
		{"s", nil},
	}
	for _, c := range cases {
		got, ok := ResolveClass(c.in)
		if ok != (c.want != nil) || !cmp.Equal(got, c.want) {
			t.Errorf("ResolveClass(%q) = %q, %v; want %q", c.in, got, ok, c.want)
		}
	}
}

func TestResolveClasses(t *testing.T) {
	t.Parallel()
	got, ok := ResolveClasses([]string{"dog", "animal", "face", "dogs"})
	want := []string{"dog", "bird", "cat", "horse", "sheep", "cow", "elephant", "bear", "zebra", "giraffe", "face"}
	if !ok || !cmp.Equal(got, want) {
		t.Errorf("ResolveClasses = %q, %v; want %q", got, ok, want)
	}
	if got, ok := ResolveClasses(nil); !ok || got != nil {
		t.Errorf("ResolveClasses(nil) = %q, %v", got, ok)
	}
	if _, ok := ResolveClasses([]string{"dog", "nope"}); ok {
		t.Error("unknown class resolved")
	}
}

func TestObjectClasses(t *testing.T) {
	t.Parallel()
	c := ObjectClasses()
	if len(c) != NumObjectClasses || c[0] != "person" || c[79] != "toothbrush" {
		t.Fatalf("ObjectClasses: %d %q %q", len(c), c[0], c[len(c)-1])
	}
	for i, name := range c {
		if ObjectClassID(name) != i || CanonicalClass(name) != name {
			t.Errorf("class %d %q is not canonical or not found", i, name)
		}
		if got, ok := ResolveClass(name); !ok || len(got) != 1 || got[0] != name {
			t.Errorf("ResolveClass(%q) = %q", name, got)
		}
	}
	if ObjectClassID("face") != -1 {
		t.Error("face is not an object class")
	}
}
