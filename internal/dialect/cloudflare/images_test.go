package cloudflare_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/xantiagoma/xtandard-image/internal/dialect/cloudflare"
)

const delivery = "/" + unpicAccount + "/" + unpicImage

// TestDelivery covers the Cloudflare Images delivery grammar: variants,
// flexible variants (inline options), accounts, ids and signed URLs.
func TestDelivery(t *testing.T) {
	t.Parallel()
	signed := func(path, query string) string {
		q, err := getFixture(t).cf.SignCloudflareQuery(path, query)
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	runGolden(t, "delivery", []testCase{
		// Variants.
		{name: "variant_preset", cfg: "images", path: delivery + "/thumbnail"},
		{name: "variant_preset_q", cfg: "images", path: delivery + "/mobile"},
		{name: "variant_public_default", cfg: "images", path: delivery + "/public"},
		{name: "variant_missing", cfg: "images", path: delivery},
		{name: "variant_unknown", cfg: "images", path: delivery + "/hero"},
		{name: "variant_no_presets", cfg: "images_bare", path: delivery + "/thumbnail"},
		{name: "variant_public_no_presets", cfg: "images_bare", path: delivery + "/public"},
		// Flexible variants.
		{name: "flexible", cfg: "images", path: delivery + "/w=400,sharpen=3"},
		{name: "flexible_all", cfg: "images", path: delivery + "/width=400,height=300,fit=cover,gravity=auto,quality=80,format=avif,dpr=2,metadata=keep"},
		{name: "flexible_onerror_ignored", cfg: "images", path: delivery + "/w=10,onerror=redirect"},
		{name: "flexible_invalid", cfg: "images", path: delivery + "/w=abc"},
		{name: "flexible_unknown_strict", cfg: "images_strict", path: delivery + "/w=10,bogus=1"},
		{name: "flexible_only_presets", cfg: "images_only", path: delivery + "/w=10"},
		{name: "variant_only_presets", cfg: "images_only", path: delivery + "/thumbnail"},
		// Paths.
		{name: "native_prefix", cfg: "images", path: "/cdn-cgi/imagedelivery" + delivery + "/public"},
		{name: "custom_id_slashes", cfg: "images", path: "/" + unpicAccount + "/products/2026/shoe.jpg/thumbnail"},
		{name: "custom_id_escaped", cfg: "images", path: "/" + unpicAccount + "/a%20b.jpg/public"},
		{name: "missing_id", cfg: "images", path: "/" + unpicAccount},
		{name: "missing_id_slash", cfg: "images", path: "/" + unpicAccount + "/"},
		{name: "empty_variant", cfg: "images", path: delivery + "/"},
		{name: "empty_segment", cfg: "images", path: "/" + unpicAccount + "//x/public"},
		{name: "empty_path", cfg: "images", path: ""},
		// Accounts.
		{name: "account_mapped", cfg: "images_accounts", path: delivery + "/public"},
		{name: "account_unknown", cfg: "images_accounts", path: "/otherhash/" + unpicImage + "/public"},
		// Signed URLs.
		{name: "signed", cfg: "images_signed", path: delivery + "/public", query: signed(delivery+"/public", "exp="+expFuture)},
		{name: "signed_flexible", cfg: "images_signed", path: delivery + "/w=100", query: signed(delivery+"/w=100", "exp="+expFuture)},
		{name: "signed_no_exp", cfg: "images_signed", path: delivery + "/public", query: signed(delivery+"/public", "")},
		{name: "signed_custom_domain", cfg: "images_signed", path: "/cdn-cgi/imagedelivery" + delivery + "/public", query: signed(delivery+"/public", "exp="+expFuture)},
		{name: "signed_expired", cfg: "images_signed", path: delivery + "/public", query: signed(delivery+"/public", "exp="+expPast)},
		{name: "signed_tampered_exp", cfg: "images_signed", path: delivery + "/public", query: "exp=" + expFuture + "&sig=" + signed(delivery+"/public", "exp="+expPast)[len("exp="+expPast+"&sig="):]},
		{name: "signed_tampered_variant", cfg: "images_signed", path: delivery + "/thumbnail", query: signed(delivery+"/public", "exp="+expFuture)},
		{name: "signed_bad_hex", cfg: "images_signed", path: delivery + "/public", query: "exp=" + expFuture + "&sig=zz"},
		{name: "signed_duplicate_sig", cfg: "images_signed", path: delivery + "/public", query: "sig=a&sig=b"},
		{name: "unsigned_allowed", cfg: "images_signed", path: delivery + "/public"},
		{name: "unsigned_required", cfg: "images_required", path: delivery + "/public"},
		{name: "exp_without_signing", cfg: "images", path: delivery + "/public", query: "exp=" + expPast},
		{name: "exp_invalid", cfg: "images", path: delivery + "/public", query: "exp=soon"},
		{name: "exp_duplicate", cfg: "images", path: delivery + "/public", query: "exp=" + expFuture + "&exp=" + expFuture},
		{name: "exp_huge", cfg: "images", path: delivery + "/public", query: "exp=99999999999999"},
	})
}

func TestSplitDeliveryPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want cloudflare.DeliveryURL
		bad  bool
	}{
		{path: "/h/i/v", want: cloudflare.DeliveryURL{AccountHash: "h", ImageID: "i", Variant: "v"}},
		{path: "/h/i", want: cloudflare.DeliveryURL{AccountHash: "h", ImageID: "i"}},
		{path: "/h/a/b/c/v", want: cloudflare.DeliveryURL{AccountHash: "h", ImageID: "a/b/c", Variant: "v"}},
		{path: "/h/i/w=1,h=2", want: cloudflare.DeliveryURL{AccountHash: "h", ImageID: "i", Variant: "w=1,h=2"}},
		{path: "/h", bad: true},
		{path: "/h/", bad: true},
		{path: "//i/v", bad: true},
		{path: "/h//v", bad: true},
		{path: "/h/i/", bad: true},
		{path: "", bad: true},
	}
	for _, tc := range tests {
		got, err := cloudflare.SplitDeliveryPath(tc.path)
		if tc.bad {
			if err == nil {
				t.Errorf("SplitDeliveryPath(%q) = %+v, want error", tc.path, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("SplitDeliveryPath(%q): %v", tc.path, err)
			continue
		}
		if d := cmp.Diff(tc.want, got); d != "" {
			t.Errorf("SplitDeliveryPath(%q) (-want +got):\n%s", tc.path, d)
		}
	}
}

func TestNewImagesRejectsBadAccounts(t *testing.T) {
	t.Parallel()
	for _, acc := range []map[string]string{{"": "src"}, {"a/b": "src"}, {"hash": ""}} {
		if _, err := cloudflare.NewImages(&cloudflare.ImagesOptions{Accounts: acc}); err == nil {
			t.Errorf("NewImages(%v) succeeded", acc)
		}
	}
}
