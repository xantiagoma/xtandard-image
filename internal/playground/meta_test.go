package playground

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestSourceSyntax(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, typ          string
		hasBase, hasBucket bool
		syntax             []string
		prefix             string
	}{
		{name: "web", typ: "http", syntax: []string{"xi://web/https://…", "https://…"}, prefix: "xi://web/https://"},
		{name: "cdn", typ: "http", hasBase: true, syntax: []string{"xi://cdn/…", "https://…"}, prefix: "xi://cdn/"},
		{name: "local", typ: "fs", syntax: []string{"xi://local/…", "local:///…"}, prefix: "xi://local/"},
		{name: "assets", typ: "s3", hasBucket: true, syntax: []string{"xi://assets/…", "s3://<bucket>/…"}, prefix: "xi://assets/"},
		{name: "archive", typ: "s3", syntax: []string{"s3://<bucket>/…"}, prefix: "s3://"},
		{name: "g", typ: "gcs", hasBucket: true, syntax: []string{"xi://g/…", "gs://<bucket>/…"}, prefix: "xi://g/"},
		{name: "blobs", typ: "azure", syntax: []string{"abs://<container>/…"}, prefix: "abs://"},
		{name: "files", typ: "sftp", syntax: []string{"xi://files/…", "sftp://<host>/…"}, prefix: "xi://files/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			syntax, prefix := SourceSyntax(tc.name, tc.typ, tc.hasBase, tc.hasBucket)
			if diff := cmp.Diff(tc.syntax, syntax); diff != "" {
				t.Errorf("syntax mismatch (-want +got):\n%s", diff)
			}
			if prefix != tc.prefix {
				t.Errorf("prefix = %q, want %q", prefix, tc.prefix)
			}
		})
	}
}
