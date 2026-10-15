package xtandard_test

import (
	"errors"
	"fmt"

	xtandard "github.com/xantiagoma/xtandard-image/clients/go"
)

func ExampleSign() {
	// Keys and salts are hex, as in XI_SIGNING_KEYS / XI_SIGNING_SALTS.
	key, err := xtandard.ParseSigningKey("736563726574", "68656c6c6f", xtandard.DefaultSignatureSize)
	if err != nil {
		panic(err)
	}
	fmt.Println(xtandard.SignImgproxyPath("/rs:fill:300:400:0/g:sm/plain/http://example.com/a.jpg", key))
	// Output: /WUaKGyrYbFTAQJLrUXRs13k9YOtEhz3zAJHrlt24vMw/rs:fill:300:400:0/g:sm/plain/http://example.com/a.jpg
}

func ExampleSignXiURL() {
	key, _ := xtandard.ParseSigningKey("736563726574", "68656c6c6f", xtandard.DefaultSignatureSize)
	url, err := xtandard.SignXiURL("https://img.example.com/q/?url=s3://b/a.jpg&w=300", key, xtandard.QueryMount)
	if err != nil {
		panic(err)
	}
	fmt.Println(url)
	// Output: https://img.example.com/q/?url=s3://b/a.jpg&w=300&xi_sig=2TQa9xNK6Mz0EY8sZmemvh8wL6oLXQzQQ_xLQ0QGM_A
}

func ExampleEncryptSource() {
	// A fixed IV makes the token deterministic; leave it empty in production
	// for a random IV per token.
	key, err := xtandard.ParseEncryptionKey(
		"1eb5b0e971ad7f45324c1bb15c947cb207c43152fa5c6c7f35c4f36e0c18e0f1",
		"30313233343536373839616263646566",
	)
	if err != nil {
		panic(err)
	}
	token, _ := xtandard.EncryptSource("http://img.example.com/pretty/image.jpg", key)
	fmt.Println(xtandard.EncryptedSourcePrefix + token)
	// Output: enc/MDEyMzQ1Njc4OWFiY2RlZj206ZwwVKiBydg5ip7OI2oVBFCdcMXXGze1K8CjwiQuFTiJkTDmRJeJccTs0wH06w
}

func ExampleXiSource() {
	src, _ := xtandard.XiSource("photos", "2026/cat.jpg")
	fmt.Println(src)
	_, err := xtandard.XiSource("bad name", "cat.jpg")
	fmt.Println(errors.Is(err, xtandard.ErrInvalidName))
	// Output:
	// xi://photos/2026/cat.jpg
	// true
}

func ExampleRangeHeader() {
	for _, r := range []xtandard.Range{
		xtandard.ByteRange{Start: 0, End: 1023},
		xtandard.ByteRangeFrom{Start: 100},
		xtandard.ByteSuffix{Length: 500},
	} {
		h, _ := xtandard.RangeHeader(r)
		fmt.Println(h)
	}
	// Output:
	// bytes=0-1023
	// bytes=100-
	// bytes=-500
}

func ExampleBuildError() {
	_, err := xtandard.EncryptPDFPassword("", xtandard.EncryptionKey{})
	if be, ok := errors.AsType[*xtandard.BuildError](err); ok {
		fmt.Println(be.Code, errors.Is(err, xtandard.ErrInvalidPassword))
	}
	// Output: invalid_password true
}

func ExampleCatalogue() {
	c := xtandard.CatalogueProcessing
	canonical, _ := c.Canonical("rs")
	fmt.Println(canonical, c.Short("quality"))
	_, err := c.Written("nope", xtandard.ShortNames)
	fmt.Println(errors.Is(err, xtandard.ErrUnknownOption))
	// Output:
	// resize q
	// true
}
