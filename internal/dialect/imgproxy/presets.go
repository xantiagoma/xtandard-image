// Copyright 2017 Sergey Alexandrovich (imgproxy), Apache License 2.0.
// Preset expansion reimplemented from imgproxy options/parser (apply.go
// applyPresetOption, processing_options.go parsePathPresets) and the
// chained-pipelines preset rules of its docs. Modifications copyright
// xtandard-image contributors, MIT. See NOTICE.

package imgproxy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/preset"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const expPreset = "defined preset"

// ErrOnlyPresets is the cause of the 403 for a URL that contains more than
// preset references while only-presets mode is on.
var ErrOnlyPresets = errors.New("imgproxy: only presets are allowed")

func onlyPresetsError() error {
	return &errs.Error{
		Kind:   errs.KindForbidden,
		Public: "only presets are allowed",
		Err:    ErrOnlyPresets,
	}
}

// validatePresets applies every preset of o.Presets to an empty request, as
// imgproxy does at startup, so a bad option fails New instead of every
// request. Unknown option names are rejected even when Strict is off.
func (p *Parser) validatePresets() error {
	s := p.o.Presets
	if s == nil {
		return nil
	}
	if s.Separator() != p.sep {
		return fmt.Errorf("imgproxy: presets use separator %q, dialect uses %q", s.Separator(), p.sep)
	}
	for _, name := range s.Names() {
		st := newState(p, false)
		if err := st.applyPresets([]string{name}); err != nil {
			return fmt.Errorf("imgproxy: preset %q: %w", name, err)
		}
		for _, ig := range st.r.Ignored {
			if ig.Reason == reasonUnknown {
				return fmt.Errorf("imgproxy: preset %q: unknown option %q", name, ig.Option)
			}
		}
	}
	return nil
}

// applyPresets handles the preset (pr) option: each named preset is
// expanded in place. Without a store (Options.Presets nil), and in /info
// URLs (imgproxy resolves those against separate info presets, which we do
// not have yet), the names are only recorded in Delivery.Presets.
func (st *state) applyPresets(names []string) error {
	for _, name := range names {
		if name == "" {
			return dialect.InvalidArgument("preset", name, expNonEmpty)
		}
		if st.p.o.Presets == nil || st.info {
			st.r.Delivery.Presets = append(st.r.Delivery.Presets, name)
			continue
		}
		pr, ok := st.p.o.Presets.Get(name)
		if !ok {
			return dialect.InvalidArgument("preset", name, expPreset)
		}
		if err := st.expandPreset(pr); err != nil {
			return err
		}
	}
	return nil
}

// expandPreset applies pr at the current pipeline: its first pipeline to
// the current one, its i-th chained pipeline to the i-th pipeline after it
// (created when missing). The current pipeline is unchanged afterwards, so
// a later "-" in the URL moves into a pipeline the preset may have created
// and URL options there override the preset's.
func (st *state) expandPreset(pr *preset.Preset) error {
	st.r.Delivery.Presets = append(st.r.Delivery.Presets, pr.Name)
	base := st.cur
	defer func() { st.cur = base }()
	for i, opts := range pr.Pipelines {
		if err := st.selectPipeline(base + i); err != nil {
			return err
		}
		for _, o := range opts {
			st.cur = base + i // a nested preset restores it, but be explicit
			if err := st.apply(o.Name, o.Args); err != nil {
				return err
			}
		}
	}
	return nil
}

// selectPipeline makes pipeline i current, appending pipelines up to it.
func (st *state) selectPipeline(i int) error {
	for len(st.r.Pipelines) <= i {
		if len(st.r.Pipelines) >= transform.MaxPipelines {
			return dialect.InvalidURL("too many chained pipelines")
		}
		st.r.Pipelines = append(st.r.Pipelines, transform.Pipeline{})
		st.bgAlpha = append(st.bgAlpha, -1)
	}
	st.cur = i
	return nil
}

// presetPrologue runs before the URL's options: it applies the default
// preset and, in only-presets mode, consumes every option segment of rest
// (which must all be preset references) and returns the source part.
func (st *state) presetPrologue(rest string) (string, error) {
	s := st.p.o.Presets
	if def, ok := s.Default(); ok {
		if err := st.expandPreset(def); err != nil {
			return "", err
		}
	}
	if !s.Only() {
		return rest, nil
	}
	// imgproxy: the first segment is always a separator-joined preset list.
	// Later segments containing the separator must be references too; the
	// first one without it starts the source.
	for first := true; rest != ""; first = false {
		seg, tail, _ := strings.Cut(rest, "/")
		if seg == "-" {
			return "", onlyPresetsError()
		}
		if !first && !strings.Contains(seg, st.p.sep) {
			break
		}
		names := strings.Split(seg, st.p.sep)
		if _, isPreset := s.Get(names[0]); !isPreset && len(names) > 1 && (names[0] == "preset" || names[0] == "pr") {
			names = names[1:]
		}
		for _, name := range names {
			pr, ok := s.Get(name)
			if !ok {
				return "", onlyPresetsError()
			}
			if err := st.expandPreset(pr); err != nil {
				return "", err
			}
		}
		rest = tail
	}
	if rest == "" {
		return "", dialect.InvalidURL("missing source")
	}
	return rest, nil
}
