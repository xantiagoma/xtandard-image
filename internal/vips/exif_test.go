//go:build cgo

package vips

import "testing"

func TestEXIFTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ifd    int
		name   string
		want   string
		wantOK bool
	}{
		// The examples of imgproxy's /info documentation.
		{2, "DateTimeOriginal", "Date and Time (Original)", true},
		{0, "DateTime", "Date and Time", true},
		{2, "ApertureValue", "Aperture", true},
		{0, "Model", "Model", true},
		{0, "Orientation", "Orientation", true},
		{0, "XResolution", "X-Resolution", true},
		{3, "GPSVersionID", "GPS Tag Version", true}, // tag 0: exif_tag_from_name's "not found" value
		{0, "NotATag", "", false},
		{-1, "Model", "", false},
		{99, "Model", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := EXIFTitle(tc.ifd, tc.name)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("EXIFTitle(%d, %q) = %q, %v; want %q, %v", tc.ifd, tc.name, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
