package vips

import "slices"

// Arena collects the images of one request so that one deferred Close
// releases every node, on success and on every error path (ARCHITECTURE
// §8.2). Like Image it belongs to a single Run and is not goroutine-safe.
//
//	var a vips.Arena
//	defer a.Close()
//	img, err := a.Add(vips.Load(ctx, buf, opts))
type Arena struct {
	imgs []*Image
}

// Add tracks img and passes both results through, so it can wrap a call
// directly. A nil img (the error case) is not tracked.
func (a *Arena) Add(img *Image, err error) (*Image, error) {
	if img != nil {
		a.imgs = append(a.imgs, img)
	}
	return img, err
}

// Track adds an image that was created outside Add.
func (a *Arena) Track(img *Image) *Image {
	if img != nil {
		a.imgs = append(a.imgs, img)
	}
	return img
}

// Len returns the number of tracked images.
func (a *Arena) Len() int { return len(a.imgs) }

// Close closes every tracked image, newest first, and empties the arena.
func (a *Arena) Close() {
	for _, img := range slices.Backward(a.imgs) {
		img.Close()
	}
	clear(a.imgs)
	a.imgs = a.imgs[:0]
}
