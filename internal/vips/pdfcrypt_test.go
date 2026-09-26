package vips

import (
	"bytes"
	"testing"
)

func TestPDFEncrypted(t *testing.T) {
	t.Parallel()
	const head = "%PDF-1.7\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n"
	tests := []struct {
		name string
		data string
		want bool
	}{
		{name: "not a pdf", data: "trailer << /Encrypt 5 0 R >>", want: false},
		{name: "empty", data: "", want: false},
		{name: "plain", data: head + "trailer\n<< /Root 1 0 R /Size 3 >>\nstartxref\n0\n%%EOF\n", want: false},
		{
			name: "classic trailer",
			data: head + "trailer << /Root 1 0 R /Size 12 /ID [<aa><bb>] /Encrypt 11 0 R >>\n%%EOF",
			want: true,
		},
		{
			name: "offset header",
			data: "\x00\x00junk%PDF-2.0\ntrailer<</Encrypt<</Filter/Standard>>>>",
			want: true,
		},
		{
			name: "cross-reference stream",
			data: head + "12 0 obj\n<< /Type /XRef /Size 13 /W [1 2 1] /Encrypt 11 0 R /Root 1 0 R /Length 3 >>\nstream\nabc\nendstream\nendobj\n",
			want: true,
		},
		{
			name: "incremental update adds encryption",
			data: head + "trailer << /Root 1 0 R /Size 3 >>\n%%EOF\n3 0 obj\n<< /Filter /Standard >>\nendobj\ntrailer << /Root 1 0 R /Prev 9 /Encrypt 3 0 R >>\n%%EOF",
			want: true,
		},
		{
			name: "EncryptMetadata is another name",
			data: head + "trailer << /Root 1 0 R /EncryptMetadata false >>",
			want: false,
		},
		{
			name: "in a literal string",
			data: head + "trailer << /Root 1 0 R /Info (see /Encrypt \\) here) >>",
			want: false,
		},
		{
			name: "in a nested dictionary",
			data: head + "trailer << /Root 1 0 R /Info << /Encrypt 1 >> >>",
			want: false,
		},
		{
			name: "in an array",
			data: head + "trailer << /Root 1 0 R /Keys [/Encrypt] >>",
			want: false,
		},
		{
			name: "in a comment",
			data: head + "trailer << /Root 1 0 R % /Encrypt 3 0 R\n>>",
			want: false,
		},
		{
			name: "in stream data",
			data: head + "4 0 obj\n<< /Length 20 >>\nstream\nBT /Encrypt Tf ET\nendstream\nendobj\n",
			want: false,
		},
		{
			name: "no enclosing structure is conservative",
			data: "%PDF-1.4\n/Encrypt 3 0 R",
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pdfEncrypted([]byte(tc.data)); got != tc.want {
				t.Errorf("pdfEncrypted = %v, want %v", got, tc.want)
			}
		})
	}
}

func FuzzPDFEncrypted(f *testing.F) {
	for _, s := range []string{
		"%PDF-1.7\ntrailer << /Encrypt 11 0 R >>",
		"%PDF-1.7\n1 0 obj << /Type /XRef /Encrypt 2 0 R >> stream\nendstream",
		"%PDF-1.7\ntrailer << /A (x\\)/Encrypt) /B <</Encrypt 1>> >>",
		"%PDF-/Encrypt",
		"%PDF-1.7 obj<</Encrypt",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if pdfEncrypted(data) && !bytes.Contains(data, []byte("/Encrypt")) {
			t.Fatal("encrypted without an /Encrypt name")
		}
	})
}
