package imgproxy

import (
	"strconv"
	"strings"

	"github.com/xantiagoma/xtandard-image/internal/dialect"
	"github.com/xantiagoma/xtandard-image/internal/errs"
	"github.com/xantiagoma/xtandard-image/internal/transform"
)

// pdfPassword is pdf_password:<token> (pdp, WP-115): the password of a
// password-protected PDF, encrypted exactly like an enc/ source (crypt,
// AES-CBC under the XI_ENCRYPTION_KEYS key). An empty token removes it.
//
// Only a URL whose signature was verified may carry one: an unsigned or
// trusted-signature URL is refused with 403 before the token is looked at,
// so nobody can use the decryption (unauthenticated CBC) as a padding
// oracle, and a password never travels in a URL anyone could forge. The
// plaintext goes to Request.Source.Password.Secret, never into an error,
// and its keyed hash (crypt.PasswordDigest) to .Digest for the cache key.
func (st *state) pdfPassword(args []string) error {
	if !st.verified {
		return &errs.Error{
			Kind:   errs.KindForbidden,
			Msg:    "option pdf_password on a url without a verified signature",
			Public: "pdf_password requires a signed url",
			Err:    dialect.ErrOptionNotAllowed,
		}
	}
	c := st.p.o.Cipher
	if c == nil {
		return &errs.Error{
			Kind:   errs.KindForbidden,
			Msg:    "option pdf_password without XI_ENCRYPTION_KEYS",
			Public: "pdf_password is not enabled",
			Err:    dialect.ErrOptionNotAllowed,
		}
	}
	// base64url may contain a custom argument separator ("_"): the token
	// is every argument joined back.
	token := strings.Join(args, st.p.sep)
	src := &st.r.Source
	src.Password.Clear()
	src.Password = nil
	if token == "" {
		return nil
	}
	pw, err := c.DecryptPassword(token)
	if err != nil {
		return err
	}
	if len(pw) > transform.MaxPasswordLen {
		clear(pw)
		// The value is not echoed, not even internally.
		return dialect.InvalidArgument("pdf_password", "", "a password of at most "+strconv.Itoa(transform.MaxPasswordLen)+" bytes")
	}
	src.Password = &transform.DocumentPassword{Secret: pw, Digest: c.PasswordDigest(pw)}
	return nil
}
