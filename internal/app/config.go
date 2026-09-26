package app

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/xantiagoma/xtandard-image/internal/obs/logging"
)

// Config is the walking skeleton's settings: the handful of documented
// XI_* variables (docs/CONFIG.md) that WP-00 needs. It is deliberately tiny
// and temporary: WP-04 replaces it with internal/config (caarlos0/env, YAML,
// _FILE secrets) and app will map that Config into component options.
type Config struct {
	Build Build // set by cmd from -ldflags, not from the environment

	DevMode bool // XI_DEV_MODE

	ServerAddr              string        // XI_SERVER_ADDR
	ServerRequestTimeout    time.Duration // XI_SERVER_REQUEST_TIMEOUT
	ServerReadHeaderTimeout time.Duration // XI_SERVER_READ_HEADER_TIMEOUT
	ServerReadTimeout       time.Duration // XI_SERVER_READ_TIMEOUT
	ServerWriteTimeout      time.Duration // XI_SERVER_WRITE_TIMEOUT (default request timeout + 5s)
	ServerIdleTimeout       time.Duration // XI_SERVER_IDLE_TIMEOUT
	ServerMaxHeaderBytes    int           // XI_SERVER_MAX_HEADER_BYTES
	ServerShutdownDelay     time.Duration // XI_SERVER_SHUTDOWN_DELAY
	ServerShutdownTimeout   time.Duration // XI_SERVER_SHUTDOWN_TIMEOUT

	AdminAddr  string // XI_ADMIN_ADDR; "off" disables the admin listener (see adminDisabled)
	AdminPprof bool   // XI_ADMIN_PPROF

	FetchTimeout              time.Duration // XI_FETCH_TIMEOUT
	FetchAllowPrivateNetworks bool          // XI_FETCH_ALLOW_PRIVATE_NETWORKS
	LimitsMaxSrcBytes         int64         // XI_LIMITS_MAX_SRC_BYTES

	ProcessingWorkers      int           // XI_PROCESSING_WORKERS
	ProcessingQueueSize    int           // XI_PROCESSING_QUEUE_SIZE
	ProcessingQueueTimeout time.Duration // XI_PROCESSING_QUEUE_TIMEOUT

	VipsConcurrency int // XI_VIPS_CONCURRENCY
	JPEGQuality     int // XI_FORMATS_JPEG_QUALITY

	LogLevel slog.Level // XI_OBSERVABILITY_LOG_LEVEL
}

// ConfigFromEnv reads the variables above through getenv (main passes
// os.Getenv; nothing in internal/ calls it directly), applying the
// documented defaults for unset ones. It reports every malformed value.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	p := envParser{getenv: getenv}
	workers := p.int("XI_PROCESSING_WORKERS", runtime.GOMAXPROCS(0))
	requestTimeout := p.duration("XI_SERVER_REQUEST_TIMEOUT", 20*time.Second)
	c := Config{
		DevMode:                   p.bool("XI_DEV_MODE", false),
		ServerAddr:                p.string("XI_SERVER_ADDR", ":8080"),
		ServerRequestTimeout:      requestTimeout,
		ServerReadHeaderTimeout:   p.duration("XI_SERVER_READ_HEADER_TIMEOUT", 5*time.Second),
		ServerReadTimeout:         p.duration("XI_SERVER_READ_TIMEOUT", 15*time.Second),
		ServerWriteTimeout:        p.duration("XI_SERVER_WRITE_TIMEOUT", requestTimeout+5*time.Second),
		ServerIdleTimeout:         p.duration("XI_SERVER_IDLE_TIMEOUT", 90*time.Second),
		ServerMaxHeaderBytes:      p.int("XI_SERVER_MAX_HEADER_BYTES", 32<<10),
		ServerShutdownDelay:       p.duration("XI_SERVER_SHUTDOWN_DELAY", 5*time.Second),
		ServerShutdownTimeout:     p.duration("XI_SERVER_SHUTDOWN_TIMEOUT", 30*time.Second),
		AdminAddr:                 adminAddr(p.string("XI_ADMIN_ADDR", "127.0.0.1:9090")),
		AdminPprof:                p.bool("XI_ADMIN_PPROF", false),
		FetchTimeout:              p.duration("XI_FETCH_TIMEOUT", 10*time.Second),
		FetchAllowPrivateNetworks: p.bool("XI_FETCH_ALLOW_PRIVATE_NETWORKS", false),
		LimitsMaxSrcBytes:         p.int64("XI_LIMITS_MAX_SRC_BYTES", 50<<20),
		ProcessingWorkers:         workers,
		ProcessingQueueSize:       p.int("XI_PROCESSING_QUEUE_SIZE", workers*16),
		ProcessingQueueTimeout:    p.duration("XI_PROCESSING_QUEUE_TIMEOUT", 5*time.Second),
		VipsConcurrency:           p.int("XI_VIPS_CONCURRENCY", 1),
		JPEGQuality:               p.int("XI_FORMATS_JPEG_QUALITY", 80),
		LogLevel:                  p.level("XI_OBSERVABILITY_LOG_LEVEL", slog.LevelInfo),
	}
	if err := errors.Join(p.errs...); err != nil {
		return Config{}, err
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// adminDisabled is the XI_ADMIN_ADDR value that turns the admin listener
// off. CONFIG.md documents "empty"; a getenv func cannot tell empty from
// unset, so the skeleton uses "off" until WP-04's loader lands.
const adminDisabled = "off"

func adminAddr(v string) string {
	if v == adminDisabled {
		return ""
	}
	return v
}

// Validate reports cross-field and range violations. Component packages
// validate their own options again when app builds them.
func (c *Config) Validate() error {
	var list []error
	if c.ServerAddr == "" {
		list = append(list, errors.New("XI_SERVER_ADDR must not be empty"))
	}
	if c.ServerShutdownDelay < 0 || c.ServerShutdownTimeout <= 0 {
		list = append(list, errors.New("XI_SERVER_SHUTDOWN_DELAY must be >= 0 and XI_SERVER_SHUTDOWN_TIMEOUT > 0"))
	}
	if c.FetchTimeout <= 0 {
		list = append(list, errors.New("XI_FETCH_TIMEOUT must be > 0"))
	}
	if c.LimitsMaxSrcBytes <= 0 {
		list = append(list, errors.New("XI_LIMITS_MAX_SRC_BYTES must be > 0"))
	}
	if c.JPEGQuality < 1 || c.JPEGQuality > 100 {
		list = append(list, fmt.Errorf("XI_FORMATS_JPEG_QUALITY must be in [1, 100], got %d", c.JPEGQuality))
	}
	return errors.Join(list...)
}

type envParser struct {
	getenv func(string) string
	errs   []error
}

func (p *envParser) lookup(key string) (string, bool) {
	v := strings.TrimSpace(p.getenv(key))
	return v, v != ""
}

func (p *envParser) fail(key, v string, err error) {
	p.errs = append(p.errs, fmt.Errorf("%s=%q: %w", key, v, err))
}

func (p *envParser) string(key, def string) string {
	if v, ok := p.lookup(key); ok {
		return v
	}
	return def
}

func (p *envParser) bool(key string, def bool) bool {
	v, ok := p.lookup(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.fail(key, v, err)
	}
	return b
}

func (p *envParser) int(key string, def int) int {
	v, ok := p.lookup(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.fail(key, v, err)
	}
	return n
}

func (p *envParser) int64(key string, def int64) int64 {
	v, ok := p.lookup(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		p.fail(key, v, err)
	}
	return n
}

func (p *envParser) duration(key string, def time.Duration) time.Duration {
	v, ok := p.lookup(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		p.fail(key, v, err)
	}
	return d
}

func (p *envParser) level(key string, def slog.Level) slog.Level {
	v, ok := p.lookup(key)
	if !ok {
		return def
	}
	l, err := logging.ParseLevel(v)
	if err != nil {
		p.fail(key, v, err)
	}
	return l
}
