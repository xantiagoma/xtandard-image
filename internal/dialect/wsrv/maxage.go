package wsrv

import (
	"time"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
)

// maxage bounds (weserv src/nginx/util.cpp parse_max_age).
const (
	minMaxAge = 24 * time.Hour
	maxMaxAge = 365 * 24 * time.Hour
	expMaxAge = "a duration from 1d to 1y such as 31d, 1w, 2M or 86400 (seconds)"
)

// parseMaxAge parses nginx's ngx_parse_time(s, is_sec=1) syntax used by
// wsrv's maxage: a sequence of <number><unit> with units y (365 days),
// M (30 days), w, d, h, m and s in decreasing order, optionally separated
// by spaces; a trailing number without unit is seconds. The result must be
// between one day and one year. wsrv falls back to its default for
// anything else; xi rejects it (400).
func parseMaxAge(v string) (time.Duration, error) {
	bad := func() (time.Duration, error) { return 0, dialect.InvalidArgument("maxage", v, expMaxAge) }
	const maxSecs = int64(maxMaxAge / time.Second)
	var (
		total, n int64
		digits   bool
		step     = 8 // units must strictly decrease
	)
	for i := range len(v) {
		c := v[i]
		if c >= '0' && c <= '9' {
			n = n*10 + int64(c-'0')
			if n > maxSecs {
				return bad()
			}
			digits = true
			continue
		}
		if c == ' ' {
			if digits {
				return bad() // a number must be followed by its unit
			}
			continue
		}
		if !digits {
			return bad()
		}
		var unit int64
		var s int
		switch c {
		case 'y':
			unit, s = 365*86400, 7
		case 'M':
			unit, s = 30*86400, 6
		case 'w':
			unit, s = 7*86400, 5
		case 'd':
			unit, s = 86400, 4
		case 'h':
			unit, s = 3600, 3
		case 'm':
			unit, s = 60, 2
		case 's':
			unit, s = 1, 1
		default:
			return bad()
		}
		if s >= step {
			return bad()
		}
		step = s
		total += n * unit
		if total > maxSecs {
			return bad()
		}
		n, digits = 0, false
	}
	if digits {
		if step <= 1 {
			return bad()
		}
		total += n
	}
	d := time.Duration(total) * time.Second
	if total > maxSecs || d < minMaxAge {
		return bad()
	}
	return d, nil
}
