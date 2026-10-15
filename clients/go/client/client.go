// Package client is an HTTP client for xtandard-image on net/http: /info,
// processed images, /video results (with Range and HEAD), HLS and DASH
// manifests, the health endpoints and the playground's metadata. URLs are
// built (and signed) with an [imgproxy.Builder].
//
// Every call takes a context first. Media calls return the *http.Response
// with its body unread, so it can be streamed: close it. A non-2xx response is
// an [*HTTPError] (status, the server's error kind, message and request ID);
// a request that got no response is a [*TransportError]. URL-building faults
// are *xtandard.BuildError values, as everywhere in the module.
//
// A Client is safe for concurrent use.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
	"github.com/xantiagoma/xtandard-image/clients/go/imgproxy"
)

// Config configures a [Client].
type Config struct {
	// URLs configures the URLs the client builds. URLs.BaseURL is the
	// server's origin (plus the imgproxy mount prefix, if any) and is required.
	URLs imgproxy.Config
	// HTTPClient sends the requests; nil uses http.DefaultClient. Timeouts
	// belong on the context of each call (or on this client's Timeout, which
	// also bounds reading a streamed body).
	HTTPClient *http.Client
	// Header is sent with every request (an API gateway's key, say).
	Header http.Header
	// PlaygroundBaseURL is the origin of the admin listener for
	// [Client.PlaygroundMeta]; empty means URLs.BaseURL.
	PlaygroundBaseURL string
	// PlaygroundPath is XI_PLAYGROUND_PATH; empty means "/_playground/".
	PlaygroundPath string
}

// Client talks to one xtandard-image server.
type Client struct {
	base           string
	urls           *imgproxy.Builder
	http           *http.Client
	header         http.Header
	playgroundMeta string
}

// maxErrorBody bounds how much of an error response is read.
const maxErrorBody = 64 << 10

// maxManifest bounds an HLS or DASH manifest.
const maxManifest = 16 << 20

var scheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

// New returns a Client. A relative or empty URLs.BaseURL is an error, as is
// an invalid separator ([xtandard.ErrInvalidSeparator]).
func New(cfg Config) (*Client, error) {
	if !scheme.MatchString(cfg.URLs.BaseURL) {
		return nil, fmt.Errorf("xtandard/client: base URL %q is not absolute", cfg.URLs.BaseURL)
	}
	urls, err := imgproxy.New(cfg.URLs)
	if err != nil {
		return nil, err
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	base := urls.Config().BaseURL
	origin := strings.TrimRight(cfg.PlaygroundBaseURL, "/")
	if origin == "" {
		origin = base
	}
	path := strings.Trim(cfg.PlaygroundPath, "/")
	if path == "" {
		path = "_playground"
	}
	return &Client{
		base:           base,
		urls:           urls,
		http:           hc,
		header:         cfg.Header.Clone(),
		playgroundMeta: origin + "/" + path + "/api/meta",
	}, nil
}

// URLs returns the client's URL builder.
func (c *Client) URLs() *imgproxy.Builder { return c.urls }

// RequestOption changes one media request.
type RequestOption func(*request)

type request struct {
	method string
	accept string
	rng    xtandard.Range
	header http.Header
}

// Head sends HEAD instead of GET: the headers (type, length, cache
// validators) without the body.
func Head() RequestOption { return func(r *request) { r.method = http.MethodHead } }

// WithRange asks for part of the body (206 Partial Content).
func WithRange(rng xtandard.Range) RequestOption { return func(r *request) { r.rng = rng } }

// Accept sets the Accept header ("*/*" by default); for images it drives
// format negotiation ("image/avif,image/webp").
func Accept(value string) RequestOption { return func(r *request) { r.accept = value } }

// WithHeader adds a request header.
func WithHeader(key, value string) RequestOption {
	return func(r *request) {
		if r.header == nil {
			r.header = http.Header{}
		}
		r.header.Add(key, value)
	}
}

func (c *Client) abs(url string) string {
	if scheme.MatchString(url) {
		return url
	}
	if !strings.HasPrefix(url, "/") {
		url = "/" + url
	}
	return c.base + url
}

func (c *Client) do(ctx context.Context, url string, opts []RequestOption) (*http.Response, error) {
	r := request{method: http.MethodGet, accept: "*/*"}
	for _, o := range opts {
		o(&r)
	}
	req, err := http.NewRequestWithContext(ctx, r.method, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("xtandard/client: %w", err)
	}
	for k, v := range c.header {
		req.Header[k] = append([]string(nil), v...)
	}
	for k, v := range r.header {
		req.Header[k] = append(req.Header[k], v...)
	}
	req.Header.Set("Accept", r.accept)
	if r.rng != nil {
		h, err := r.rng.RangeHeader()
		if err != nil {
			return nil, err
		}
		req.Header.Set("Range", h)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, &TransportError{Method: r.method, URL: url, Err: err}
	}
	return res, nil
}

// checked returns res for a 2xx status; otherwise it reads (part of) the
// body, closes it and returns an *HTTPError.
func checked(res *http.Response) (*http.Response, error) {
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res, nil
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	return nil, newHTTPError(res, body)
}

// Media sends GET (or HEAD) for any URL or server path: an image or video
// URL, a query dialect URL, a stream part. The caller closes the body.
func (c *Client) Media(ctx context.Context, url string, opts ...RequestOption) (*http.Response, error) {
	res, err := c.do(ctx, c.abs(url), opts)
	if err != nil {
		return nil, err
	}
	return checked(res)
}

// Image fetches a processed image. Accept drives format negotiation.
func (c *Client) Image(ctx context.Context, source string, p imgproxy.Params, opts ...RequestOption) (*http.Response, error) {
	url, err := c.urls.URL(source, p)
	if err != nil {
		return nil, err
	}
	return c.Media(ctx, url, opts...)
}

// Video fetches a /video result: video, audio or a waveform. WithRange asks
// for part of it (206), Head for its headers.
func (c *Client) Video(ctx context.Context, source string, p imgproxy.VideoParams, opts ...RequestOption) (*http.Response, error) {
	url, err := c.urls.VideoURL(source, p)
	if err != nil {
		return nil, err
	}
	return c.Media(ctx, url, opts...)
}

// StreamManifest is a fetched HLS playlist or DASH MPD.
type StreamManifest struct {
	// URL is the (signed) manifest URL; hand it to a player.
	URL string
	// ContentType is application/vnd.apple.mpegurl or application/dash+xml.
	ContentType string
	// Body is the manifest. Its child URLs (/_stream/<ticket>/…) carry a
	// ticket and need no signing: fetch them with [Client.Media].
	Body string
}

// Stream fetches an HLS or DASH manifest (/video/…@m3u8 or .mpd).
func (c *Client) Stream(ctx context.Context, source string, p imgproxy.StreamParams, opts ...RequestOption) (*StreamManifest, error) {
	url, err := c.urls.StreamURL(source, p)
	if err != nil {
		return nil, err
	}
	_, ctype, _ := p.Protocol.Manifest()
	res, err := c.Media(ctx, url, append([]RequestOption{Accept(ctype + ", */*;q=0.5")}, opts...)...)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxManifest+1))
	if err != nil {
		return nil, &TransportError{Method: http.MethodGet, URL: url, Err: err}
	}
	if len(body) > maxManifest {
		return nil, &TransportError{Method: http.MethodGet, URL: url, Err: errors.New("manifest larger than 16 MiB")}
	}
	if h := res.Header.Get("Content-Type"); h != "" {
		ctype = h
	}
	return &StreamManifest{URL: url, ContentType: ctype, Body: string(body)}, nil
}

func (c *Client) json(ctx context.Context, url string, v any) error {
	res, err := c.Media(ctx, url, Accept("application/json"))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(v); err != nil {
		return &TransportError{Method: http.MethodGet, URL: url, Err: fmt.Errorf("decoding the JSON body: %w", err)}
	}
	return nil
}

// Info fetches /info for a source, with the URL built (and signed) from the params.
func (c *Client) Info(ctx context.Context, source string, p imgproxy.InfoParams) (*xtandard.InfoResponse, error) {
	url, err := c.urls.InfoURL(source, p)
	if err != nil {
		return nil, err
	}
	return c.InfoAt(ctx, url)
}

// InfoAt fetches a prebuilt /info URL or path.
func (c *Client) InfoAt(ctx context.Context, url string) (*xtandard.InfoResponse, error) {
	var v xtandard.InfoResponse
	if err := c.json(ctx, c.abs(url), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Healthz returns nil while the process can serve HTTP (GET /healthz).
func (c *Client) Healthz(ctx context.Context) error {
	res, err := c.Media(ctx, c.base+"/healthz", Accept("text/plain"))
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxErrorBody))
	return res.Body.Close()
}

// ReadyState is the state /readyz reports.
type ReadyState string

// The readiness states.
const (
	Ready    ReadyState = "ready"
	Starting ReadyState = "starting"
	Draining ReadyState = "draining"
)

// Readiness is the /readyz answer.
type Readiness struct {
	// Ready is true only for 200 ready.
	Ready bool
	// State is ready, starting or draining.
	State ReadyState
}

// Readyz returns the readiness state (GET /readyz); 503 (starting, draining) is not an error.
func (c *Client) Readyz(ctx context.Context) (Readiness, error) {
	url := c.base + "/readyz"
	res, err := c.do(ctx, url, []RequestOption{Accept("text/plain")})
	if err != nil {
		return Readiness{}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusServiceUnavailable {
		return Readiness{}, newHTTPError(res, body)
	}
	state := ReadyState(strings.TrimSpace(string(body)))
	switch state {
	case Ready, Starting, Draining:
	default:
		e := newHTTPError(res, body)
		e.Message = fmt.Sprintf("unexpected readiness state %q", state)
		return Readiness{}, e
	}
	return Readiness{Ready: res.StatusCode == http.StatusOK && state == Ready, State: state}, nil
}

// PlaygroundMeta fetches the playground's api/meta (needs
// XI_PLAYGROUND_ENABLED; served on the admin listener by default).
func (c *Client) PlaygroundMeta(ctx context.Context) (map[string]any, error) {
	var v map[string]any
	if err := c.json(ctx, c.playgroundMeta, &v); err != nil {
		return nil, err
	}
	return v, nil
}
