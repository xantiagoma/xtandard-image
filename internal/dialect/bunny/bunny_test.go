package bunny_test

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/dialect/bunny"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/security/sign"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

const img = "/a.jpg"

// TestOptions covers every Optimizer parameter (docs/URL-DIALECTS.md §9)
// with valid and invalid values.
func TestOptions(t *testing.T) {
	t.Parallel()
	runGolden(t, "options", []testCase{
		// Source.
		{name: "plain", path: img},
		{name: "nested_path", path: "/img/2024/a%20b.jpg"},
		{name: "empty_path", path: "/"},
		{name: "no_path", path: ""},
		{name: "scheme_like_key", path: "/https:/evil.example.com/a.jpg"},
		{name: "bad_path_escape", path: "/a%zz.jpg"},
		{name: "control_char", path: "/a%00.jpg"},
		{name: "bad_query_escape", path: img, query: "width=%zz"},
		// Resize.
		{name: "width", path: img, query: "width=400"},
		{name: "height", path: img, query: "height=300"},
		{name: "width_height", path: img, query: "width=100&height=75"},
		{name: "width_zero", path: img, query: "width=0"},
		{name: "width_negative", path: img, query: "width=-1"},
		{name: "width_float", path: img, query: "width=1.5"},
		{name: "width_huge", path: img, query: "width=99999999"},
		{name: "height_invalid", path: img, query: "height=abc"},
		{name: "width_last_wins", path: img, query: "width=100&width=200"},
		{name: "upscaling_resampling", path: img, query: "width=2048&upscaling=resampling"},
		{name: "upscaling_off", path: img, query: "width=2048&upscaling=off"},
		{name: "upscaling_invalid", path: img, query: "upscaling=on"},
		// Crop.
		{name: "crop_wh", path: img, query: "crop=800,600"},
		{name: "crop_wh_gravity", path: img, query: "crop=400,300&crop_gravity=north"},
		{name: "crop_whxy", path: img, query: "crop=400,300,200,150"},
		{name: "crop_then_resize", path: img, query: "crop=800,600&width=400"},
		{name: "crop_three_args", path: img, query: "crop=1,2,3"},
		{name: "crop_zero", path: img, query: "crop=0,10"},
		{name: "crop_relative_rejected", path: img, query: "crop=0.5,0.5"},
		{name: "crop_invalid", path: img, query: "crop=a,b"},
		{name: "crop_gravity_center", path: img, query: "crop=400,300&crop_gravity=center"},
		{name: "crop_gravity_south", path: img, query: "crop=400,300&crop_gravity=south"},
		{name: "crop_gravity_east", path: img, query: "crop=400,300&crop_gravity=east"},
		{name: "crop_gravity_west", path: img, query: "crop=400,300&crop_gravity=west"},
		{name: "crop_gravity_northeast", path: img, query: "crop=400,300&crop_gravity=northeast"},
		{name: "crop_gravity_northwest", path: img, query: "crop=400,300&crop_gravity=northwest"},
		{name: "crop_gravity_southeast", path: img, query: "crop=400,300&crop_gravity=southeast"},
		{name: "crop_gravity_southwest", path: img, query: "crop=400,300&crop_gravity=southwest"},
		{name: "crop_gravity_top_left", path: img, query: "crop=400,300&crop_gravity=top_left"},
		{name: "crop_gravity_bottom", path: img, query: "crop=400,300&crop_gravity=bottom"},
		{name: "crop_gravity_invalid", path: img, query: "crop_gravity=middle"},
		{name: "aspect_ratio", path: img, query: "aspect_ratio=16:9"},
		{name: "aspect_ratio_width", path: img, query: "aspect_ratio=16:9&width=256"},
		{name: "aspect_ratio_gravity", path: img, query: "aspect_ratio=1:1&crop_gravity=west"},
		{name: "aspect_ratio_with_crop", path: img, query: "crop=800,600&aspect_ratio=1:1"},
		{name: "aspect_ratio_decimal", path: img, query: "aspect_ratio=1.5:1"},
		{name: "aspect_ratio_auto", path: img, query: "aspect_ratio=auto"},
		{name: "aspect_ratio_zero", path: img, query: "aspect_ratio=0:9"},
		{name: "aspect_ratio_no_colon", path: img, query: "aspect_ratio=16"},
		{name: "aspect_ratio_extreme", path: img, query: "aspect_ratio=100000:1"},
		{name: "aspect_ratio_matches_box", path: img, query: "width=200&height=100&aspect_ratio=200:100&crop_gravity=north"},
		{name: "aspect_ratio_matches_box_upscaling", path: img, query: "width=200&height=100&aspect_ratio=2:1&upscaling=resampling"},
		{name: "aspect_ratio_differs_from_box", path: img, query: "width=200&height=100&aspect_ratio=1:1"},
		{name: "focus_crop_absolute", path: img, query: "focus_crop=400,300,500,400"},
		{name: "focus_crop_absolute_edge", path: img, query: "focus_crop=400,300,10,10"},
		{name: "focus_crop_relative", path: img, query: "focus_crop=400,300,0.5,0.5"},
		{name: "focus_crop_relative_out_of_range", path: img, query: "focus_crop=400,300,1.5,0.5"},
		{name: "focus_crop_invalid", path: img, query: "focus_crop=400,300"},
		{name: "face_crop_true", path: img, query: "face_crop=true"},
		{name: "face_crop_false", path: img, query: "face_crop=false"},
		{name: "face_crop_wh", path: img, query: "face_crop=600,600"},
		{name: "face_crop_invalid", path: img, query: "face_crop=big"},
		// Reflections and rotations.
		{name: "flip", path: img, query: "flip=true"},
		{name: "flip_false", path: img, query: "flip=false"},
		{name: "flop", path: img, query: "flop=true"},
		{name: "flip_flop", path: img, query: "flip=true&flop=1"},
		{name: "flip_invalid", path: img, query: "flip=yes"},
		{name: "rotate_90", path: img, query: "rotate=90"},
		{name: "rotate_minus_90", path: img, query: "rotate=-90"},
		{name: "rotate_270", path: img, query: "rotate=270"},
		{name: "rotate_minus_270", path: img, query: "rotate=-270"},
		{name: "rotate_invalid", path: img, query: "rotate=45"},
		{name: "rotate_too_big", path: img, query: "rotate=360"},
		// Filters.
		{name: "blur", path: img, query: "blur=25"},
		{name: "blur_zero", path: img, query: "blur=0"},
		{name: "blur_out_of_range", path: img, query: "blur=101"},
		{name: "sharpen_true", path: img, query: "sharpen=true"},
		{name: "sharpen_false", path: img, query: "sharpen=false"},
		{name: "sharpen_numeric", path: img, query: "sharpen=3"},
		{name: "sharpen_invalid", path: img, query: "sharpen=very"},
		// Colour and luminosity.
		{name: "brightness", path: img, query: "brightness=30"},
		{name: "brightness_negative", path: img, query: "brightness=-30"},
		{name: "brightness_zero", path: img, query: "brightness=0"},
		{name: "brightness_out_of_range", path: img, query: "brightness=101"},
		{name: "gamma", path: img, query: "gamma=40"},
		{name: "gamma_negative", path: img, query: "gamma=-100"},
		{name: "contrast", path: img, query: "contrast=15"},
		{name: "contrast_minimum", path: img, query: "contrast=-100"},
		{name: "saturation_grayscale", path: img, query: "saturation=-100"},
		{name: "saturation", path: img, query: "saturation=15"},
		{name: "saturation_invalid", path: img, query: "saturation=x"},
		{name: "hue", path: img, query: "hue=33"},
		{name: "hue_negative", path: img, query: "hue=-1"},
		{name: "tint", path: img, query: "tint=aaffff"},
		{name: "tint_white", path: img, query: "tint=ffffff"},
		{name: "tint_invalid", path: img, query: "tint=blue"},
		{name: "sepia", path: img, query: "sepia=75"},
		{name: "sepia_zero", path: img, query: "sepia=0"},
		{name: "sepia_invalid", path: img, query: "sepia=200"},
		// Format and quality.
		{name: "format_webp", path: img, query: "format=webp"},
		{name: "format_jpeg", path: img, query: "format=jpeg"},
		{name: "format_png", path: img, query: "format=png"},
		{name: "format_gif", path: img, query: "format=gif"},
		{name: "format_avif", path: img, query: "format=avif"},
		{name: "format_upper", path: img, query: "format=WEBP"},
		{name: "format_invalid", path: img, query: "format=tiff"},
		{name: "output_webp", path: img, query: "output=webp"},
		{name: "quality", path: img, query: "quality=75"},
		{name: "quality_zero", path: img, query: "quality=0"},
		{name: "quality_invalid", path: img, query: "quality=101"},
		{name: "auto_optimize_low", path: img, query: "auto_optimize=low"},
		{name: "auto_optimize_medium", path: img, query: "auto_optimize=medium"},
		{name: "auto_optimize_high", path: img, query: "auto_optimize=high"},
		{name: "auto_optimize_explicit_quality", path: img, query: "auto_optimize=high&quality=90"},
		{name: "auto_optimize_invalid", path: img, query: "auto_optimize=max"},
		// Classes.
		{name: "class_unexpanded", path: img, query: "class=thumbnail"},
		{name: "class", cfg: "presets", path: img, query: "class=thumbnail"},
		{name: "class_override", cfg: "presets", path: img, query: "class=thumbnail&quality=50&width=100"},
		{name: "class_unknown", cfg: "presets", path: img, query: "class=nope"},
		{name: "class_empty", cfg: "presets", path: img, query: "class="},
		{name: "class_only", cfg: "only_presets", path: img, query: "class=hero"},
		{name: "class_only_extra_option", cfg: "only_presets", path: img, query: "class=hero&width=10"},
		{name: "class_default_preset", cfg: "default_pre", path: img, query: "class=hero"},
		// Everything else.
		{name: "optimizer", path: img, query: "optimizer=image&width=10"},
		{name: "unknown", path: img, query: "utm_source=x&width=10"},
		{name: "unknown_strict", cfg: "strict", path: img, query: "utm_source=x"},
		{name: "not_enforced", path: img, query: "token_countries=US&token_countries_blocked=CN&limit=100"},
		{name: "combined", path: img, query: "width=800&crop=600,400&brightness=5&sharpen=true&quality=80"},
		{name: "combined_docs", path: img, query: "aspect_ratio=16:9&gamma=10&saturation=10&format=webp"},
	})
}

// TestTokens covers token authentication through the dialect, with the
// SDK's signed URLs (BunnyWay/BunnyCDN.TokenAuthentication e2e/vectors.json
// at 48ff1af and the C# tests at 1019b23) and expiry handling.
func TestTokens(t *testing.T) {
	t.Parallel()
	const (
		hsPlain     = "token=HS256-o10JRWlsAItyAsdKS6jJKjabHN4FrFsplDHPV1idcX4&expires=1598024587"
		hsPath      = "token=HS256-uVZvT3SbEoVKYJyDJgbcsDmSFf73cv-uNUVaJiKWpbQ&token_path=%2Fabc&expires=1598024587"
		hsCountries = "token=HS256-i5s3Uv7mnrFfN5nznwU2BxhILpnipYeE18TapV0WNFM&token_countries=CA%2CUS&expires=1598024587"
		hsIgnore    = "token=HS256-1lwWBD_c1IAGSj1UKPoxreu8ePDQ-Z9FoWLcRn_RRH0&token_ignore_params=true&expires=1598024587"
		hsIP        = "token=HS256-1-L2rISTLcujMY9UFf2tbZ41d5i-Bme1g1oTK_Z2QMLJk&expires=1598024587"
		shaPath     = "token=xwPaUzEMSgOZ7yl86K55G7len9n1UMiuP36IAyw8Mjs&token_path=%2Fabc&expires=1598024587"
	)
	runGolden(t, "tokens", []testCase{
		{name: "hs256", cfg: "signed", path: "/300kb.jpg", query: hsPlain},
		{name: "hs256_required", cfg: "required", path: "/300kb.jpg", query: hsPlain},
		{name: "hs256_token_path", cfg: "signed", path: "/abc/300kb.jpg", query: hsPath},
		{name: "hs256_token_path_outside", cfg: "signed", path: "/xyz/300kb.jpg", query: hsPath},
		{name: "hs256_countries", cfg: "signed", path: "/300kb.jpg", query: hsCountries},
		{name: "hs256_ignore_params_extra_option", cfg: "signed", path: "/300kb.jpg", query: hsIgnore + "&width=100"},
		{name: "hs256_ip_locked", cfg: "signed", path: "/300kb.jpg", query: hsIP},
		{name: "hs256_added_option", cfg: "signed", path: "/300kb.jpg", query: hsPlain + "&width=100"},
		{name: "hs256_wrong_path", cfg: "signed", path: "/301kb.jpg", query: hsPlain},
		{name: "hs256_layout_only", cfg: "hs256_only", path: "/abc/300kb.jpg", query: shaPath},
		{name: "sha256_token_path", cfg: "signed", path: "/abc/300kb.jpg", query: shaPath},
		{
			name: "sha256_path_token_directory", cfg: "signed",
			path: "/bcdn_token=e0fYj-NC_YeROS_0gTGvscP7HR_Du78I7WBVSDV8P4E&expires=1598024587/abc/",
		},
		{
			name: "sha256_path_token_token_path", cfg: "signed",
			path: "/bcdn_token=xwPaUzEMSgOZ7yl86K55G7len9n1UMiuP36IAyw8Mjs&token_path=%2Fabc&expires=1598024587/abc/",
		},
		{name: "path_token_bad_escape", cfg: "signed", path: "/bcdn_token=%zz/a.jpg"},
		{name: "unsigned_optional", cfg: "signed", path: img, query: "width=10"},
		{name: "unsigned_required", cfg: "required", path: img, query: "width=10"},
		{name: "token_without_expires", cfg: "signed", path: "/300kb.jpg", query: "token=HS256-o10JRWlsAItyAsdKS6jJKjabHN4FrFsplDHPV1idcX4"},
		{name: "duplicate_token", cfg: "signed", path: "/300kb.jpg", query: hsPlain + "&token=x"},
		{name: "duplicate_signed_param", cfg: "signed", path: "/300kb.jpg", query: hsPlain + "&width=1&width=2"},
		{name: "signing_off_token_ignored", path: "/300kb.jpg", query: hsPlain},
		{name: "expired_unsigned", path: img, query: "expires=1000"},
		{name: "future_unsigned", path: img, query: "expires=4102444800"},
		{name: "expires_invalid", path: img, query: "expires=soon"},
	})
}

// TestTokenRoundTrip signs Optimizer URLs with every layout and checks the
// dialect verifies them, rejects tampering and enforces expiry.
func TestTokenRoundTrip(t *testing.T) {
	t.Parallel()
	f := getFixture(t)
	future := strconv.FormatInt(now.Unix()+3600, 10)
	past := strconv.FormatInt(now.Unix()-1, 10)
	params := []sign.BunnyParam{{Key: "width", Value: "400"}, {Key: "aspect_ratio", Value: "16:9"}, {Key: "class", Value: "a b"}}
	query := "width=400&aspect_ratio=16%3A9&class=a+b"
	for _, tc := range []struct {
		name    string
		signer  *sign.Signer
		token   string
		expires string
		query   string
		wantErr errs.Kind
	}{
		{name: "hs256", signer: f.hs256, token: "HS256-", expires: future, query: query},
		{name: "sha256", signer: f.sha256, token: strings.Repeat("A", 43), expires: future, query: query},
		{name: "basic", signer: f.basic, token: "A", expires: future, query: query},
		{name: "basic_ignores_options", signer: f.basic, token: "A", expires: future, query: query + "&blur=10"},
		{name: "hs256_tampered", signer: f.hs256, token: "HS256-", expires: future, query: "width=4000&aspect_ratio=16:9&class=a+b", wantErr: errs.KindUnauthorized},
		{name: "hs256_expired", signer: f.hs256, token: "HS256-", expires: past, query: query, wantErr: errs.KindExpired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			msg, err := sign.BunnyMessage(tc.token, "/dir/a.jpg", tc.expires, params)
			if err != nil {
				t.Fatal(err)
			}
			token, err := tc.signer.Sign(msg)
			if err != nil {
				t.Fatal(err)
			}
			q := tc.query + "&token=" + url.QueryEscape(token) + "&expires=" + tc.expires
			res, err := parse(t, "required", "/dir/a.jpg", q)
			if tc.wantErr != 0 {
				if errs.KindOf(err) != tc.wantErr {
					t.Fatalf("err = %v, want kind %v", err, tc.wantErr)
				}
				if tc.wantErr == errs.KindExpired && !errors.Is(err, dialect.ErrExpired) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if res.Signature != sign.Verified {
				t.Fatalf("Signature = %v", res.Signature)
			}
			// The same token in path form verifies too.
			pathQ := "bcdn_token=" + url.QueryEscape(token) + "&expires=" + tc.expires
			res, err = parse(t, "required", "/"+pathQ+"/dir/a.jpg", tc.query)
			if err != nil || res.Signature != sign.Verified || res.SourceString != "dir/a.jpg" {
				t.Fatalf("path token: %v, %v", res, err)
			}
		})
	}
}

// TestUnpic parses the URLs unpic's bunny provider produces
// (.refs/unpic/src/providers/bunny.test.ts) and checks them against the
// meaning unpic documents for each parameter. width, height and a matching
// aspect_ratio (unpic's transform) is a fill-down resize; without
// aspect_ratio the image fits inside the box (Bunny keeps the aspect ratio).
// The zero ResizeType is fit.
func TestUnpic(t *testing.T) {
	t.Parallel()
	const base = "/bunny7.jpg" // https://bunnyoptimizerdemo.b-cdn.net/bunny7.jpg
	type want struct {
		width, height int
		rtype         transform.ResizeType
		gravity       transform.GravityType
		quality       int
		format        transform.Format
	}
	for _, tc := range []struct {
		name  string
		query string
		want  want
	}{
		{"transform_width_height", "width=200&height=100&aspect_ratio=200:100", want{width: 200, height: 100, rtype: transform.ResizeFillDown}},
		{"transform_width_only", "width=200", want{width: 200}},
		{"transform_rounded", "width=201&height=100&aspect_ratio=201:100", want{width: 201, height: 100, rtype: transform.ResizeFillDown}},
		{"generate_width_height", "width=200&height=100", want{width: 200, height: 100}},
		{"generate_crop_gravity", "crop_gravity=bottom&width=400&height=300", want{width: 400, height: 300, gravity: transform.GravitySouth}},
		{"generate_quality", "width=600&quality=80", want{width: 600, quality: 80}},
		{"generate_format", "output=webp&width=400", want{width: 400, format: transform.FormatWebP}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := parse(t, "strict", base, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			r := res.Request
			pl := r.Pipelines[0]
			got := want{
				width: pl.Resize.Width.Px, height: pl.Resize.Height.Px,
				gravity: pl.Gravity.Type, quality: r.Output.Quality, format: r.Output.Format,
			}
			got.rtype = pl.Resize.Type
			if d := cmp.Diff(tc.want, got, cmp.AllowUnexported(want{})); d != "" {
				t.Errorf("(-want +got):\n%s", d)
			}
			if res.SourceString != "bunny7.jpg" || len(r.Ignored) != 0 || pl.Crop != nil {
				t.Errorf("source %q, ignored %v", res.SourceString, r.Ignored)
			}
		})
	}
}

func TestVerifierAccessor(t *testing.T) {
	t.Parallel()
	p, err := bunny.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Verifier() != nil || p.Name() != bunny.Name {
		t.Error("zero parser has a verifier")
	}
	if getFixture(t).parsers["signed"].Verifier() == nil {
		t.Error("signed parser has no verifier")
	}
	if _, err := bunny.New(&bunny.Options{ArgumentsSeparator: "/"}); err == nil {
		t.Error("bad separator accepted")
	}
}
