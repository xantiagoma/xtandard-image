package imageinfo

// Options selects the pixel-derived fields Analyze computes (the
// imgproxy info options palette, average, dominant_colors, blurhash,
// thumb_hash and perceptual_hash).
type Options struct {
	// Palette is the palette size, 0 (off) or 2..256.
	Palette int
	// Average turns on the average colour; AverageIgnoreTransparent
	// leaves fully transparent pixels out of it.
	Average, AverageIgnoreTransparent bool
	// Dominant turns on the dominant colours; DominantBuildMissed builds
	// the classes the image lacks.
	Dominant, DominantBuildMissed bool
	// BlurhashX and BlurhashY are the BlurHash components (1..9 each);
	// 0 turns it off.
	BlurhashX, BlurhashY int
	Thumbhash            bool
	PerceptualHash       bool
}

// Any reports whether o asks for any field, that is whether the image
// must be decoded at all.
func (o *Options) Any() bool {
	return o != nil && (o.Palette > 0 || o.Average || o.Dominant || o.BlurhashX > 0 || o.Thumbhash || o.PerceptualHash)
}

// Result holds the fields Options asked for; the others are zero.
type Result struct {
	Palette        []Color
	Average        *Color
	Dominant       *DominantColors
	Blurhash       string
	Thumbhash      []byte
	PerceptualHash *uint64
}

// Analyze computes the fields o asks for on m, first fitting m inside
// MaxSide (a no-op for the copy internal/app hands over, which vips has
// already bounded).
func Analyze(m *Image, o *Options) (*Result, error) {
	res := &Result{}
	if !o.Any() {
		return res, nil
	}
	m, err := Downscale(m, MaxSide)
	if err != nil {
		return nil, err
	}
	if o.Palette > 0 {
		if res.Palette, err = Palette(m, o.Palette); err != nil {
			return nil, err
		}
	}
	if o.Average {
		c, err := Average(m, o.AverageIgnoreTransparent)
		if err != nil {
			return nil, err
		}
		res.Average = &c
	}
	if o.Dominant {
		d, err := Dominant(m, o.DominantBuildMissed)
		if err != nil {
			return nil, err
		}
		res.Dominant = &d
	}
	if o.BlurhashX > 0 || o.BlurhashY > 0 {
		if res.Blurhash, err = Blurhash(m, o.BlurhashX, o.BlurhashY); err != nil {
			return nil, err
		}
	}
	if o.Thumbhash {
		small, err := Downscale(m, MaxThumbhashSide)
		if err != nil {
			return nil, err
		}
		if res.Thumbhash, err = Thumbhash(small); err != nil {
			return nil, err
		}
	}
	if o.PerceptualHash {
		h, err := PerceptualHash(m)
		if err != nil {
			return nil, err
		}
		res.PerceptualHash = &h
	}
	return res, nil
}
