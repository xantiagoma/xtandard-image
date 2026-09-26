package s3cache

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestNewValidates(t *testing.T) {
	t.Parallel()
	if _, err := New(&Options{Bucket: "b"}); err == nil {
		t.Error("nil client accepted")
	}
	if _, err := New(&Options{Client: s3.New(s3.Options{Region: "us-east-1"})}); err == nil {
		t.Error("empty bucket accepted")
	}
	s, err := New(&Options{Client: s3.New(s3.Options{Region: "us-east-1"}), Bucket: "b", Prefix: "c/"})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.objectKey("res/v1/k"); got != "c/res/v1/k" {
		t.Errorf("object key %q", got)
	}
}

func TestDecodeMetaRejectsJunk(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "!!!", "bm90IGpzb24"} {
		if _, err := decodeMeta(v); err == nil {
			t.Errorf("decodeMeta(%q) accepted", v)
		}
	}
}
