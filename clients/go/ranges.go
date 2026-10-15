package xtandard

import "strconv"

// Range is a byte range of a Range request header: [ByteRange],
// [ByteRangeFrom], [ByteSuffix] or a [RawRange] written as given.
type Range interface {
	// RangeHeader returns the Range header value, or an [ErrInvalidRange] error.
	RangeHeader() (string, error)
}

// ByteRange is bytes Start to End, both inclusive (bytes=Start-End).
type ByteRange struct{ Start, End int64 }

// ByteRangeFrom is the bytes from Start to the end (bytes=Start-).
type ByteRangeFrom struct{ Start int64 }

// ByteSuffix is the last Length bytes (bytes=-Length).
type ByteSuffix struct{ Length int64 }

// RawRange is a Range header value written as given ("bytes=0-99,200-299").
type RawRange string

func nonNegative(n int64, what string) error {
	if n < 0 {
		return Errorf(ErrInvalidRange, "range %s %d is negative", what, n)
	}
	return nil
}

// RangeHeader implements [Range].
func (r ByteRange) RangeHeader() (string, error) {
	if err := nonNegative(r.Start, "start"); err != nil {
		return "", err
	}
	if err := nonNegative(r.End, "end"); err != nil {
		return "", err
	}
	if r.End < r.Start {
		return "", Errorf(ErrInvalidRange, "range end %d is before start %d", r.End, r.Start)
	}
	return "bytes=" + strconv.FormatInt(r.Start, 10) + "-" + strconv.FormatInt(r.End, 10), nil
}

// RangeHeader implements [Range].
func (r ByteRangeFrom) RangeHeader() (string, error) {
	if err := nonNegative(r.Start, "start"); err != nil {
		return "", err
	}
	return "bytes=" + strconv.FormatInt(r.Start, 10) + "-", nil
}

// RangeHeader implements [Range].
func (r ByteSuffix) RangeHeader() (string, error) {
	if err := nonNegative(r.Length, "suffix"); err != nil {
		return "", err
	}
	return "bytes=-" + strconv.FormatInt(r.Length, 10), nil
}

// RangeHeader implements [Range].
func (r RawRange) RangeHeader() (string, error) { return string(r), nil }

// RangeHeader formats a byte range as a Range header value
// (bytes=0-1023, bytes=100-, bytes=-500).
func RangeHeader(r Range) (string, error) { return r.RangeHeader() }
