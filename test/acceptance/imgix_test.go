//go:build acceptance

package acceptance

import (
	"crypto/md5" //nolint:gosec // imgix's s parameter is MD5 by specification
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// imgixHost is the host compose.acceptance.yml binds to the imgix dialect
// (XI_DIALECTS_IMGIX_HOSTS); its prefix is /imgix/ and its source is the
// local fs source (testdata/fixtures).
const imgixHost = "images.acceptance.test"

// imgixSign returns query with imgix's s appended: hex(MD5(token ‖ path ‖
// "?" ‖ query)), path being the escaped path after the mount prefix
// (https://github.com/imgix/imgix-blueprint#securing-urls).
func imgixSign(token, path, query string) string {
	msg := token + path
	if query != "" {
		msg += "?" + query
	}
	sum := md5.Sum([]byte(msg)) //nolint:gosec // see the import
	s := "s=" + hex.EncodeToString(sum[:])
	if query == "" {
		return s
	}
	return query + "&" + s
}

// TestImgix: the imgix dialect mounted under /imgix/ and host-bound, with
// a secure URL token. Unsigned URLs fall under the restricted policy;
// signed ones are verified (s, expires) and may use every parameter.
func TestImgix(t *testing.T) {
	t.Parallel()
	app := base(t, "XI_ACC_APP")
	token := env(t, "XI_ACC_IMGIX_TOKEN")
	const key = "/jpeg/baseline.jpg" // 320x240 on the mount's source
	signed := func(path, query string) string { return "/imgix" + path + "?" + imgixSign(token, path, query) }
	b64 := base64.RawURLEncoding.EncodeToString
	past := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	webProxy := "/" + url.PathEscape(jpegSrc)
	hostBound := http.Header{"Host": {imgixHost}, "Accept": {"image/avif,image/webp,*/*"}}
	tampered := strings.Replace(signed(key, "w=65&fm=png"), "w=65", "w=66", 1)

	runImageCases(t, app, []imageCase{
		{name: "unsigned allowlisted width", path: "/imgix" + key + "?w=64&fm=png", status: 200, ctype: "image/png", props: props{Format: "png", Width: 64, Height: 48}},
		{name: "unsigned unpic default", path: "/imgix" + key + "?w=128&fit=min&auto=format", header: http.Header{"Accept": {"image/webp"}}, status: 200, ctype: "image/webp", props: props{Format: "webp", Width: 128, Height: 96}},
		{name: "unsigned width not allowlisted", path: "/imgix" + key + "?w=65", status: 403},
		{name: "unsigned blur rejected", path: "/imgix" + key + "?w=64&blur=40", status: 403},
		{name: "unsigned watermark rejected", path: "/imgix" + key + "?w=64&txt=hi", status: 403},
		{name: "host-bound auto=format", path: key + "?w=64&auto=format,compress", header: hostBound, status: 200, ctype: "image/avif", props: props{Format: "avif", Width: 64, Height: 48}},
		{name: "signed any width", path: signed(key, "w=65&fm=png"), status: 200, ctype: "image/png", props: props{Format: "png", Width: 65, Height: 49}},
		{name: "signed crop entropy webp", path: signed(key, "w=200&h=100&fit=crop&crop=entropy&fm=webp"), status: 200, ctype: "image/webp", props: props{Format: "webp", Width: 200, Height: 100}},
		{name: "signed host-bound", path: key + "?" + imgixSign(token, key, "w=65&fm=png"), header: hostBound, status: 200, ctype: "image/png", props: props{Format: "png", Width: 65, Height: 49}},
		{name: "signed text watermark", path: signed(key, "w=200&fm=png&txt64="+b64([]byte("Hello, imgix"))+"&txt-size=24&txt-color=fff"), status: 200, ctype: "image/png", props: props{Format: "png", Width: 200, Height: 150}},
		{name: "signed image watermark", path: signed(key, "w=200&fm=png&mark64="+b64([]byte("png/rgba8.png"))+"&mark-scale=25"), status: 200, ctype: "image/png", props: props{Format: "png", Width: 200, Height: 150}},
		{name: "signed web proxy source", path: signed(webProxy, "w=64&fm=png"), status: 200, ctype: "image/png", props: props{Format: "png", Width: 64, Height: 48}},
		{name: "signed json info", path: signed(key, "fm=json"), status: 200, ctype: "application/json"},
		{name: "signed expires future", path: signed(key, "w=65&fm=png&expires="+future), status: 200, ctype: "image/png"},
		{name: "signed expired", path: signed(key, "w=65&expires="+past), status: 404},
		{name: "tampered", path: tampered, status: 403},
		{name: "bad signature", path: "/imgix" + key + "?w=64&s=" + strings.Repeat("0", 32), status: 403},
	})
}
