package config

import (
	"slices"
	"strings"
	"testing"
)

// FuzzParseByteSize: never panics, never negative, and String round-trips.
// Seeds: f.Add below plus testdata/fuzz/FuzzParseByteSize.
func FuzzParseByteSize(f *testing.F) {
	for _, s := range []string{"0", "10MiB", "32KiB", "8388607TiB", "9223372036854775807", "10MB", " 1 gib", "-1", "1e3"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, err := ParseByteSize(s)
		if err != nil {
			return
		}
		if v < 0 {
			t.Fatalf("ParseByteSize(%q) = %d", s, v)
		}
		back, err := ParseByteSize(v.String())
		if err != nil || back != v {
			t.Fatalf("round trip %q → %d → %q → %d, %v", s, v, v.String(), back, err)
		}
	})
}

// FuzzParseDuration: never panics, never negative, String round-trips.
// Seeds: f.Add below plus testdata/fuzz/FuzzParseDuration.
func FuzzParseDuration(f *testing.F) {
	for _, s := range []string{"0", "5s", "1m30s", "31536000s", "-1s", "2562047h47m16.854775807s", "1.5h", "5"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDuration(s)
		if err != nil {
			return
		}
		if d < 0 {
			t.Fatalf("ParseDuration(%q) = %v", s, d)
		}
		back, err := ParseDuration(d.String())
		if err != nil || back != d {
			t.Fatalf("round trip %q → %v → %v, %v", s, d, back, err)
		}
	})
}

// FuzzParseCIDR: never panics; accepted prefixes are canonical (masked, no
// zone, no 4in6) and their String parses back to the same prefix.
// Seeds: f.Add below plus testdata/fuzz/FuzzParseCIDR.
func FuzzParseCIDR(f *testing.F) {
	for _, s := range []string{"10.0.0.0/8", "10.0.0.1/8", "fd00::1", "fe80::1%eth0", "::ffff:10.0.0.1", "0.0.0.0/0", "::/0", "1.2.3.4/32"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParseCIDR(s)
		if err != nil {
			return
		}
		if !p.IsValid() || p != p.Masked() || p.Addr().Zone() != "" || p.Addr().Is4In6() {
			t.Fatalf("ParseCIDR(%q) = %v is not canonical", s, p)
		}
		back, err := ParseCIDR(p.String())
		if err != nil || back != p {
			t.Fatalf("round trip %q → %v → %v, %v", s, p, back, err)
		}
	})
}

// FuzzParseIntList: never panics, bounded length, String round-trips.
// Seeds: f.Add below plus testdata/fuzz/FuzzParseIntList.
func FuzzParseIntList(f *testing.F) {
	for _, s := range []string{"16,32", "5-100/5", "1-3,10", "0-9999", "1-1000000", "5-1", "1/2", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		l, err := ParseIntList(s)
		if err != nil {
			return
		}
		if len(l) > maxIntListLen {
			t.Fatalf("ParseIntList(%q) has %d values", s, len(l))
		}
		back, err := ParseIntList(l.String())
		if err != nil || !equalInts(back, l) {
			t.Fatalf("round trip %q → %v → %q → %v, %v", s, l, l.String(), back, err)
		}
	})
}

func equalInts(a, b IntList) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// FuzzLoadEnv feeds one arbitrary XI_ variable to the loader: it must never
// panic, and a value that fails to parse must not appear in the error when
// the variable is a secret.
// Seeds: f.Add below plus testdata/fuzz/FuzzLoadEnv.
func FuzzLoadEnv(f *testing.F) {
	f.Add("SERVER_ADDR", ":1")
	f.Add("SIGNING_KEYS", "zz-not-hex")
	f.Add("PRESETS", "a=b,c")
	f.Add("SOURCES", "a,b")
	f.Add("FETCH_ALLOWED_NETWORKS", "10.0.0.0/8")
	f.Add("SERVER_REQUEST_TIMEOUT", "-5s")
	f.Fuzz(func(t *testing.T, key, value string) {
		if strings.ContainsAny(key, "=\x00") {
			return
		}
		_, _, err := load(opts(nil, "XI_"+key+"="+value, "XI_SOURCES_A_TYPE=http"))
		if err != nil && strings.HasPrefix(key, "SIGNING_KEYS") && len(value) > 8 && strings.Contains(err.Error(), value) {
			t.Fatalf("error leaks the key value: %v", err)
		}
	})
}

// FuzzImgproxyEnv: translating any value of any mapped IMGPROXY_* variable
// never panics, and a successful translation only sets declared targets
// (or XI_SOURCES_* of the compat sources).
func FuzzImgproxyEnv(f *testing.F) {
	vars := imgproxyVars()
	for i, s := range []string{"true", "10", "16.8", "https://*.example.com/img/,s3://b/", "jpeg=80,png=5", "a=b;c*=d", "/data/x.png", "-1", ""} {
		f.Add(uint(i), s)
	}
	f.Fuzz(func(t *testing.T, idx uint, v string) {
		iv := vars[idx%uint(len(vars))]
		env := []string{iv.Name + "=" + v, "IMGPROXY_USE_S3=true", "IMGPROXY_LOCAL_FILESYSTEM_ROOT=/data"}
		c := translateImgproxyEnv(env)
		if c.err != nil {
			return
		}
		for k := range c.out {
			if !strings.HasPrefix(k, envSources) && !slices.Contains(iv.Targets, k) {
				t.Fatalf("%s=%q set undeclared %s", iv.Name, v, k)
			}
		}
		_, _, _ = load(LoadOptions{Environ: append(env, compatOn), ReadFile: files{}.read, GOMAXPROCS: 1})
	})
}
