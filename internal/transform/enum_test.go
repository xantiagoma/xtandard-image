package transform

import (
	"encoding"
	"errors"
	"strconv"
	"testing"
)

// enumCase checks one enum type: every value in [0, count) has a unique name
// that round-trips through Parse and through MarshalText/UnmarshalText, and
// count itself is out of range.
type enumCase struct {
	name  string
	check func(t *testing.T)
}

func enumCheck[T enum, PT interface {
	*T
	encoding.TextUnmarshaler
}](typeName string, count uint8, parse func(string) (T, error),
) enumCase {
	return enumCase{name: typeName, check: func(t *testing.T) {
		t.Helper()
		seen := map[string]bool{}
		for i := range count {
			v := T(i)
			s := v.String()
			if seen[s] {
				t.Errorf("%s(%d): duplicate name %q", typeName, i, s)
			}
			seen[s] = true

			m, ok := any(v).(encoding.TextMarshaler)
			if !ok {
				t.Fatalf("%s does not implement encoding.TextMarshaler", typeName)
			}
			text, err := m.MarshalText()
			if err != nil {
				t.Errorf("%s(%d).MarshalText: %v", typeName, i, err)
			}
			var back T
			if err := PT(&back).UnmarshalText(text); err != nil || back != v {
				t.Errorf("%s(%d): UnmarshalText(%q) = %v, %v", typeName, i, text, back, err)
			}

			if s == "" {
				if i != 0 {
					t.Errorf("%s(%d): only the zero value may have an empty name", typeName, i)
				}
				if _, err := parse(""); err == nil {
					t.Errorf("Parse%s(\"\") succeeded", typeName)
				}
				continue
			}
			got, err := parse(s)
			if err != nil || got != v {
				t.Errorf("Parse%s(%q) = %v, %v; want %v", typeName, s, got, err, v)
			}
		}

		bad := T(count)
		if want := typeName + "(" + strconv.Itoa(int(count)) + ")"; bad.String() != want {
			t.Errorf("%s(count).String() = %q, want %q (is the count constant stale?)", typeName, bad.String(), want)
		}
		if bm, ok := any(bad).(encoding.TextMarshaler); !ok {
			t.Fatalf("%s does not implement encoding.TextMarshaler", typeName)
		} else if _, err := bm.MarshalText(); err == nil {
			t.Errorf("%s(count).MarshalText succeeded", typeName)
		}
		_, err := parse("definitely-not-a-" + typeName)
		if ee, ok := errors.AsType[*EnumError](err); !ok || ee.Enum != typeName {
			t.Errorf("Parse%s(junk) error = %v, want *EnumError for %s", typeName, err, typeName)
		}
		var dst T
		if err := PT(&dst).UnmarshalText([]byte("junk!")); err == nil {
			t.Errorf("%s.UnmarshalText(junk) succeeded", typeName)
		}
	}}
}

func allEnumCases() []enumCase {
	return []enumCase{
		enumCheck[Kind]("Kind", numKinds, ParseKind),
		enumCheck[SourceEncoding]("SourceEncoding", numSourceEncodings, ParseSourceEncoding),
		enumCheck[HashAlgorithm]("HashAlgorithm", numHashAlgorithms, ParseHashAlgorithm),
		enumCheck[ResizeType]("ResizeType", numResizeTypes, ParseResizeType),
		enumCheck[Kernel]("Kernel", numKernels, ParseKernel),
		enumCheck[GravityType]("GravityType", numGravityTypes, ParseGravityType),
		enumCheck[ExtendMode]("ExtendMode", numExtendModes, ParseExtendMode),
		enumCheck[MaskShape]("MaskShape", numMaskShapes, ParseMaskShape),
		enumCheck[Direction]("Direction", numDirections, ParseDirection),
		enumCheck[UnsharpMode]("UnsharpMode", numUnsharpModes, ParseUnsharpMode),
		enumCheck[TileMode]("TileMode", numTileModes, ParseTileMode),
		enumCheck[Format]("Format", numFormats, ParseFormat),
		enumCheck[Subsampling]("Subsampling", numSubsamplings, ParseSubsampling),
		enumCheck[AutoQualityMethod]("AutoQualityMethod", numAutoQualityMethods, ParseAutoQualityMethod),
		enumCheck[WebPCompression]("WebPCompression", numWebPCompressions, ParseWebPCompression),
		enumCheck[WebPPreset]("WebPPreset", numWebPPresets, ParseWebPPreset),
		enumCheck[ResponseEncoding]("ResponseEncoding", numResponseEncodings, ParseResponseEncoding),
		enumCheck[InfoSchema]("InfoSchema", numInfoSchemas, ParseInfoSchema),
		enumCheck[GainMapMode]("GainMapMode", numGainMapModes, ParseGainMapMode),
		enumCheck[VideoFit]("VideoFit", numVideoFits, ParseVideoFit),
		enumCheck[VideoGravity]("VideoGravity", numVideoGravities, ParseVideoGravity),
		enumCheck[VideoContainer]("VideoContainer", numVideoContainers, ParseVideoContainer),
		enumCheck[VideoCodec]("VideoCodec", numVideoCodecs, ParseVideoCodec),
		enumCheck[AudioCodec]("AudioCodec", numAudioCodecs, ParseAudioCodec),
		enumCheck[VideoTimecode]("VideoTimecode", numVideoTimecodes, ParseVideoTimecode),
	}
}

func TestEnumRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range allEnumCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.check(t)
		})
	}
}

func TestParseFormatAliases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want Format
	}{
		{"jpg", FormatJPEG},
		{"jpeg", FormatJPEG},
		{"tif", FormatTIFF},
		{"tiff", FormatTIFF},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseFormat(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("ParseFormat(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
			}
			var u Format
			if err := u.UnmarshalText([]byte(tc.in)); err != nil || u != tc.want {
				t.Fatalf("UnmarshalText(%q) = %v, %v; want %v", tc.in, u, err, tc.want)
			}
		})
	}
}

func TestParseIsCaseSensitive(t *testing.T) {
	t.Parallel()
	if _, err := ParseResizeType("FIT"); err == nil {
		t.Fatal(`ParseResizeType("FIT") succeeded; parsers accept canonical lowercase only`)
	}
}

func TestEnumErrorTruncatesValue(t *testing.T) {
	t.Parallel()
	long := string(make([]byte, 10_000))
	_, err := ParseKind(long)
	ee, ok := errors.AsType[*EnumError](err)
	if !ok {
		t.Fatalf("error %v is not *EnumError", err)
	}
	if len(ee.Value) > maxEnumErrorValue {
		t.Fatalf("EnumError.Value has %d bytes, want ≤ %d", len(ee.Value), maxEnumErrorValue)
	}
}

func TestGravityTypePredicates(t *testing.T) {
	t.Parallel()
	for i := range numGravityTypes {
		g := GravityType(i)
		compass := g >= GravityCenter && g <= GravitySouthWest
		if g.IsCompass() != compass {
			t.Errorf("%v.IsCompass() = %v", g, g.IsCompass())
		}
		if compass && g.IsDetection() {
			t.Errorf("%v is both compass and detection", g)
		}
	}
	if GravityFocusPoint.IsDetection() || !GravitySmart.IsDetection() {
		t.Error("IsDetection: focus-point must be false and smart true")
	}
}

// parsedEnum lets the fuzzer treat every parser uniformly.
type parsedEnum struct {
	name  string
	parse func(string) (string, error) // returns String() of the parsed value
}

func wrapParse[T enum](name string, p func(string) (T, error)) parsedEnum {
	return parsedEnum{name: name, parse: func(s string) (string, error) {
		v, err := p(s)
		return v.String(), err
	}}
}

func FuzzParseEnums(f *testing.F) {
	parsers := []parsedEnum{
		wrapParse("Kind", ParseKind),
		wrapParse("SourceEncoding", ParseSourceEncoding),
		wrapParse("HashAlgorithm", ParseHashAlgorithm),
		wrapParse("ResizeType", ParseResizeType),
		wrapParse("Kernel", ParseKernel),
		wrapParse("GravityType", ParseGravityType),
		wrapParse("ExtendMode", ParseExtendMode),
		wrapParse("MaskShape", ParseMaskShape),
		wrapParse("Direction", ParseDirection),
		wrapParse("UnsharpMode", ParseUnsharpMode),
		wrapParse("TileMode", ParseTileMode),
		wrapParse("Format", ParseFormat),
		wrapParse("Subsampling", ParseSubsampling),
		wrapParse("AutoQualityMethod", ParseAutoQualityMethod),
		wrapParse("WebPCompression", ParseWebPCompression),
		wrapParse("WebPPreset", ParseWebPPreset),
		wrapParse("ResponseEncoding", ParseResponseEncoding),
		wrapParse("InfoSchema", ParseInfoSchema),
		wrapParse("GainMapMode", ParseGainMapMode),
	}
	for _, seed := range []string{
		"", "fit", "fill-down", "lanczos3", "north-east", "focus-point", "object-weighted",
		"jpg", "jpeg", "tif", "best", "4:4:4", "near_lossless", "data-url", "thumbor",
		"FIT", " fit", "fit\x00", "ResizeType(7)", "unset", "\xff\xfe",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, p := range parsers {
			name, err := p.parse(s)
			if err != nil {
				if _, ok := errors.AsType[*EnumError](err); !ok {
					t.Fatalf("Parse%s(%q): error %v is not *EnumError", p.name, s, err)
				}
				continue
			}
			if name == "" {
				t.Fatalf("Parse%s(%q) returned the unset value", p.name, s)
			}
			again, err := p.parse(name)
			if err != nil || again != name {
				t.Fatalf("Parse%s(%q) → %q does not round-trip: %q, %v", p.name, s, name, again, err)
			}
		}
	})
}
