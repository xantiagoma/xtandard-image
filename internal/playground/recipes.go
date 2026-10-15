package playground

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// recipesJSON is the Examples list (WP-116): ready-made sessions the page
// loads into the builder with one click. The data is checked by
// TestRecipesParse: every recipe is built the way the page builds it,
// parsed by its dialect's strict parser, and references only repository
// fixtures or samples.json URLs.
//
//go:embed recipes.json
var recipesJSON []byte

// Recipe needs (Recipe.Needs): what the server must offer for the recipe
// to render. The page disables a recipe whose needs are not met, with the
// reason in its tooltip.
const (
	// NeedWatermark: a default watermark image (XI_PROCESSING_WATERMARK_SOURCE,
	// Meta.Watermark); imgproxy wm without wmu or wmt draws it. Only the
	// imgproxy-compatibility recipe (wm-default) has it: every other
	// watermark names its image or text in the URL (WP-117).
	NeedWatermark = "watermark"
	// NeedDetection: the detection models (Meta.Detection).
	NeedDetection = "detection"
	// NeedVideo: FFmpeg is in the image (Meta.Video): video sources
	// decode and animations encode to mp4 and webm.
	NeedVideo = "video"
	// NeedVideoTransform: video transformations are served
	// (Meta.VideoTransform: XI_VIDEO_TRANSFORM_ENABLED on the full or gpl
	// image).
	NeedVideoTransform = "video_transform"
	// NeedH264 and NeedHEVC: the build encodes H.264 (libx264) or HEVC
	// (libx265), which only the gpl image has (VideoTransform.Codecs). The
	// page disables such a recipe with the reason on the full image and
	// hides it where the image has no FFmpeg at all (slim) (WP-129).
	NeedH264 = "h264"
	NeedHEVC = "hevc"
	// NeedVideoPresets: the demo's video presets (web, preview) are
	// configured (VideoTransform.Presets, XI_VIDEO_TRANSFORM_PRESETS in
	// compose.playground.yml; WP-132).
	NeedVideoPresets = "video_presets"
)

// Recipe is one ready-made session of the Examples tool.
type Recipe struct {
	// ID is unique and stable.
	ID string `json:"id" doc:"Stable identifier."`
	// Title and Description are shown on the card.
	Title       string `json:"title" doc:"Card title."`
	Description string `json:"description" doc:"What the recipe shows."`
	// Category groups the cards ("watermark", "showcase").
	Category string `json:"category" enum:"watermark,video,showcase" doc:"Card group."`
	// Dialect picks the mount: the first mount of this dialect.
	Dialect string `json:"dialect" doc:"Dialect of the mount the recipe is built on (its first mount)."`
	// Source is the source string: a repository fixture on the local fs
	// source (xi://local/<path under testdata/fixtures>) or a URL of
	// samples.json.
	Source string `json:"source" doc:"Source string: xi://local/<fixture> or a public gallery URL."`
	// Pipelines are the builder entries, one list per chained pipeline:
	// each entry is the option's catalogue name followed by its argument
	// values (a flag is its name alone). Text and URL arguments of the
	// imgproxy grammar are written plain; the page encodes them.
	Pipelines [][][]string `json:"pipelines" doc:"Builder entries per chained pipeline: [name, value…] (catalogue names; base64 arguments plain)."`
	// Ext is the imgproxy source extension (@webp; a /video container).
	Ext string `json:"ext,omitempty" doc:"imgproxy source extension (the output container of a video recipe)."`
	// Video builds the recipe on the video endpoint (/video on imgproxy
	// mounts, /q/video on query mounts) with the video option catalogue.
	Video bool `json:"video,omitempty" doc:"A video transformation: built on /video (imgproxy) or /q/video (query) from the video option catalogue."`
	// Raw writes the path by hand instead of the builder (dialects the
	// builder cannot express, such as imaginary endpoints).
	Raw *RecipeRaw `json:"raw,omitempty" doc:"Path and query written by hand ({src} is the source, {key} its key on the mount's source); the builder is bypassed."`
	// Info are Info panel entries ([name, value…]); with them the recipe
	// opens the Info tab.
	Info [][]string `json:"info,omitempty" doc:"Info panel options ([name, value…]); the recipe opens the Info (source) tab."`
	// URL is the path and query the recipe produces after the mount
	// prefix, unsigned (for imgproxy, after the signature segment).
	URL string `json:"url" doc:"Path and query after the mount prefix (and the imgproxy signature segment), unsigned."`
	// Tags are lower-case keywords for filtering.
	Tags []string `json:"tags" doc:"Lower-case keywords for the filter."`
	// Needs are server features the recipe needs (Need*).
	Needs []string `json:"needs,omitempty" enum:"watermark,detection,video,video_transform,h264,hevc,video_presets" doc:"Server features the recipe needs: watermark (a default watermark image), detection (the models), video (FFmpeg: video sources and mp4/webm output), video_transform (video transformations, XI_VIDEO_TRANSFORM_ENABLED), h264 and hevc (the gpl image's H.264 and HEVC encoders, video_transform.codecs), video_presets (the demo's video presets web and preview, video_transform.presets)."`
}

// RecipeRaw is a hand-written path and query.
type RecipeRaw struct {
	Path  string `json:"path" doc:"Path after the mount prefix; {src} is the source, {key} its key on the mount's source."`
	Query string `json:"query,omitempty" doc:"Query without ?; {src} is the source, {key} its key on the mount's source."`
}

// Recipes returns the Examples list.
func Recipes() ([]Recipe, error) {
	var r []Recipe
	if err := json.Unmarshal(recipesJSON, &r); err != nil {
		return nil, fmt.Errorf("playground: recipes.json: %w", err)
	}
	return r, nil
}
