package core

import (
	"errors"
	"math"
	"testing"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

func TestFormatNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   float64
		bits int
		want string
	}{
		{0, 64, "0"},
		{math.Copysign(0, -1), 64, "0"},
		{300, 64, "300"},
		{-90, 64, "-90"},
		{1.5, 64, "1.5"},
		{0.1, 64, "0.1"},
		{1.05, 64, "1.05"},
		{0.000001, 64, "0.000001"},
		{0.00000125, 64, "0.00000125"},
		{1e-7, 64, "1e-7"},
		{1.5e-7, 64, "1.5e-7"},
		{1e20, 64, "100000000000000000000"},
		{123456789012345680000, 64, "123456789012345680000"},
		{1e21, 64, "1e+21"},
		{2.5e25, 64, "2.5e+25"},
		{-2.5e25, 64, "-2.5e+25"},
		{math.MaxFloat64, 64, "1.7976931348623157e+308"},
		{5e-324, 64, "5e-324"},
		{float64(float32(0.1)), 32, "0.1"},
		{float64(float32(0.1)), 64, "0.10000000149011612"},
		{10000000, 64, "10000000"},
	}
	for _, tt := range tests {
		if got := FormatNumber(tt.in, tt.bits); got != tt.want {
			t.Errorf("FormatNumber(%v, %d) = %s, want %s", tt.in, tt.bits, got, tt.want)
		}
	}
}

type named string

func TestFormatArg(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   xtandard.Arg
		want string
		code xtandard.ErrorCode
	}{
		{nil, "", ""},
		{true, "1", ""},
		{false, "0", ""},
		{int8(-3), "-3", ""},
		{uint64(math.MaxUint64), "18446744073709551615", ""},
		{float32(0.5), "0.5", ""},
		{300.0, "300", ""},
		{named("fill"), "fill", ""},
		{"", "", ""},
		{"a:b", "", xtandard.ErrInvalidArgument},
		{"a/b", "", xtandard.ErrInvalidArgument},
		{"a#b", "", xtandard.ErrInvalidArgument},
		{"a\u00a0b", "", xtandard.ErrInvalidArgument},
		{"a\ufeffb", "", xtandard.ErrInvalidArgument},
		{"a\x7fb", "", xtandard.ErrInvalidArgument},
		{math.Inf(1), "", xtandard.ErrInvalidArgument},
		{math.NaN(), "", xtandard.ErrInvalidArgument},
		{[]int{1}, "", xtandard.ErrInvalidArgument},
		{struct{}{}, "", xtandard.ErrInvalidArgument},
	}
	for _, tt := range tests {
		got, err := FormatArg(tt.in, ":", "test")
		if tt.code != "" {
			if !errors.Is(err, tt.code) {
				t.Errorf("FormatArg(%#v) error = %v, want %s", tt.in, err, tt.code)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("FormatArg(%#v) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestTrim(t *testing.T) {
	t.Parallel()
	got := Trim([]xtandard.Arg{"fill", nil, named(""), ""}, 1)
	if len(got) != 1 {
		t.Errorf("Trim = %v", got)
	}
	got = Trim([]xtandard.Arg{nil, nil}, 2)
	if len(got) != 2 {
		t.Errorf("Trim below min = %v", got)
	}
	if Deref[int](nil) != nil || Deref(new(0)) != 0 {
		t.Error("Deref")
	}
}

func TestNativeValue(t *testing.T) {
	t.Parallel()
	for in, want := range map[any]string{true: "true", false: "false", 1.5: "1.5", 300: "300", "a b": "a b"} {
		if got, err := NativeValue("k", in); err != nil || got != want {
			t.Errorf("NativeValue(%v) = %q, %v", in, got, err)
		}
	}
	if _, err := NativeValue("k", []string{}); !errors.Is(err, xtandard.ErrInvalidArgument) {
		t.Errorf("slice: %v", err)
	}
	if _, err := NativeValue("k", math.Inf(-1)); !errors.Is(err, xtandard.ErrInvalidArgument) {
		t.Errorf("inf: %v", err)
	}
}
