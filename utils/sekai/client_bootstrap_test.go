package sekai

import "testing"

func TestBuildCookieHeaderFromCombinedCloudFrontSetCookie(t *testing.T) {
	t.Parallel()

	raw := "CloudFront-Policy=policy-value; Path=/; Secure; HttpOnly, CloudFront-Key-Pair-Id=key-pair-id; Path=/; Secure; HttpOnly, CloudFront-Signature=signature-value; Path=/; Secure; HttpOnly"
	got := buildCookieHeader([]string{raw})
	want := "CloudFront-Policy=policy-value; CloudFront-Key-Pair-Id=key-pair-id; CloudFront-Signature=signature-value"
	if got != want {
		t.Fatalf("buildCookieHeader() = %q, want %q", got, want)
	}
}

func TestExtractCookiePairsSkipsNonCookieCommaSegments(t *testing.T) {
	t.Parallel()

	raw := "cookie-a=value-a; Path=/, Wed, cookie-b=value-b; Path=/"
	got := extractCookiePairs(raw)
	if len(got) != 2 {
		t.Fatalf("len(extractCookiePairs()) = %d, want 2 (%v)", len(got), got)
	}
	if got[0] != "cookie-a=value-a" || got[1] != "cookie-b=value-b" {
		t.Fatalf("extractCookiePairs() = %v", got)
	}
}
