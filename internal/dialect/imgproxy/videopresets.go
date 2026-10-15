// Video presets (WP-132, ADR 0012 §8 "Video presets"). Copyright
// xtandard-image contributors, MIT. The definition syntax and the
// expansion rules are imgproxy's presets (presets.go), applied to the
// /video option table.

package imgproxy

import (
	"fmt"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// validateVideoPresets applies every video preset to an empty /video
// request, so a bad or unknown option, a chained pipeline or a delivery
// option is a startup error, not a failure of every request.
func (p *Parser) validateVideoPresets() error {
	s := p.o.VideoPresets
	if s == nil {
		return nil
	}
	if s.Separator() != p.sep {
		return fmt.Errorf("imgproxy: video presets use separator %q, dialect uses %q", s.Separator(), p.sep)
	}
	for _, name := range s.Names() {
		pr, _ := s.Get(name)
		if len(pr.Pipelines) > 1 {
			return fmt.Errorf("imgproxy: video preset %q: chained pipelines are not video options", name)
		}
		if _, _, err := p.videoPresetOp([]string{name}, false); err != nil {
			return fmt.Errorf("imgproxy: video preset %q: %w", name, err)
		}
	}
	return nil
}

// VideoPresetOp returns the VideoOp the video presets names produce on
// their own, after the "default" video preset, finished and validated as
// a /video URL holding only "pr:names..." would be. containerSet reports a
// preset that chose the output container (format or stream). It returns
// nil when there are no names and no default preset. Dialects whose video
// syntax is not imgproxy's (Cloudinary t_, ImageKit n-) start their
// VideoOp from it.
func (p *Parser) VideoPresetOp(names []string) (op *transform.VideoOp, containerSet bool, err error) {
	return p.videoPresetOp(names, true)
}

func (p *Parser) videoPresetOp(names []string, withDefault bool) (*transform.VideoOp, bool, error) {
	if len(names) == 0 && (!withDefault || !p.hasDefaultVideoPreset()) {
		return nil, false, nil
	}
	st := newVideoState(p)
	if withDefault {
		if err := st.defaultVideoPreset(); err != nil {
			return nil, false, err
		}
	}
	if err := st.applyVideoPresets(names); err != nil {
		return nil, false, err
	}
	for _, ig := range st.r.Ignored {
		if ig.Reason == reasonUnknown {
			return nil, false, fmt.Errorf("unknown option %q", ig.Option)
		}
	}
	if err := st.finishVideo(); err != nil {
		return nil, false, err
	}
	return st.r.Video, st.video.containerSet, nil
}

func (p *Parser) hasDefaultVideoPreset() bool {
	_, ok := p.o.VideoPresets.Default()
	return ok
}

// defaultVideoPreset applies the "default" video preset, if any.
func (st *state) defaultVideoPreset() error {
	if def, ok := st.p.o.VideoPresets.Default(); ok {
		return st.expandVideoPreset(def)
	}
	return nil
}

// applyVideoPresets handles the preset (pr) option of a /video URL: each
// named video preset is expanded in place, so the options after it
// override it and the options before it are overridden.
func (st *state) applyVideoPresets(names []string) error {
	for _, name := range names {
		if name == "" {
			return dialect.InvalidArgument("preset", name, expNonEmpty)
		}
		if st.p.o.VideoPresets == nil {
			// As for images: without a store the names are only recorded.
			st.r.Delivery.Presets = append(st.r.Delivery.Presets, name)
			continue
		}
		pr, ok := st.p.o.VideoPresets.Get(name)
		if !ok {
			return dialect.InvalidArgument("preset", name, expPreset)
		}
		if st.video.presetDepth == 0 {
			st.video.referenced = true
		}
		if err := st.expandVideoPreset(pr); err != nil {
			return err
		}
	}
	return nil
}

// expandVideoPreset applies pr's options (one pipeline: New refuses
// chained video presets).
func (st *state) expandVideoPreset(pr *preset.Preset) error {
	st.r.Delivery.Presets = append(st.r.Delivery.Presets, pr.Name)
	st.video.presetDepth++
	defer func() { st.video.presetDepth-- }()
	for _, o := range pr.Pipelines[0] {
		if err := st.applyVideo(o.Name, o.Args); err != nil {
			return err
		}
	}
	return nil
}

// videoPresetPrologue runs before the options of a /video URL: the
// default video preset and, in only-presets mode (XI_PRESETS_ONLY), the
// URL's preset references, which must be all of its options. It returns
// the source part.
func (st *state) videoPresetPrologue(rest string) (string, error) {
	if err := st.defaultVideoPreset(); err != nil {
		return "", err
	}
	if !st.p.o.Presets.Only() {
		return rest, nil
	}
	s := st.p.o.VideoPresets
	if s.Len() == 0 {
		return "", onlyPresetsError()
	}
	// The rules of presetPrologue: the first segment is a preset list;
	// later segments with the separator must be references too.
	for first := true; rest != ""; first = false {
		seg, tail, _ := strings.Cut(rest, "/")
		if !first && !strings.Contains(seg, st.p.sep) {
			break
		}
		names := strings.Split(seg, st.p.sep)
		if _, isPreset := s.Get(names[0]); !isPreset && len(names) > 1 && (names[0] == "preset" || names[0] == "pr") {
			names = names[1:]
		}
		for _, name := range names {
			if _, ok := s.Get(name); !ok {
				return "", onlyPresetsError()
			}
		}
		if err := st.applyVideoPresets(names); err != nil {
			return "", err
		}
		rest = tail
	}
	if rest == "" {
		return "", dialect.InvalidURL("missing source")
	}
	return rest, nil
}

// skipInPresetsOnly reports whether a presets-only parse (the VideoOp the
// URL's presets alone produce, for the restricted unsigned policy) leaves
// out the URL option name: everything but preset references.
func (st *state) skipInPresetsOnly(name string) bool {
	vs := st.video
	return vs.presetsOnly && vs.presetDepth == 0 && name != "preset" && name != "pr"
}

// presetVideo parses rest again keeping only its preset references and
// returns the VideoOp they produce, or nil when the URL references no
// video preset or that parse fails (the restricted policy then refuses
// the request).
func (p *Parser) presetVideo(rest string, referenced bool) *transform.VideoOp {
	if !referenced {
		return nil
	}
	st := newVideoState(p)
	st.video.presetsOnly = true
	if err := st.parse(rest); err != nil {
		return nil
	}
	return st.r.Video
}

// NewVideoPresetsBuilder is NewVideoBuilder for the presets-only parse of
// the restricted unsigned policy: every option but preset (pr) is
// skipped, so the request holds what the URL's video presets produce.
func (p *Parser) NewVideoPresetsBuilder(dialectName string) *Builder {
	b := p.NewVideoBuilder(dialectName)
	b.st.video.presetsOnly = true
	return b
}

// ReferencedVideoPresets reports whether the options applied so far named
// a video preset (the default preset does not count).
func (b *Builder) ReferencedVideoPresets() bool {
	return b.st.video != nil && b.st.video.referenced
}

// inPresetOption refuses the delivery and source options inside a video
// preset: a preset holds transformation options only.
func (st *state) inPresetOption(name string) error {
	if st.video.presetDepth > 0 {
		return dialect.InvalidURL("delivery option " + name + " in a video preset")
	}
	return nil
}
