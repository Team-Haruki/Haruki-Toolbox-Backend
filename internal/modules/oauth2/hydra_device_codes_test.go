package oauth2

import (
	"strings"
	"testing"
)

func TestNormalizeDeviceUserCode(t *testing.T) {
	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{"BCDFGHJK", "BCDFGHJK", true},
		{"BCDF-GHJK", "BCDFGHJK", true},
		{"bcdf-ghjk", "BCDFGHJK", true},
		{" bcdf ghjk\t", "BCDFGHJK", true},
		{"BCDF_GHJK", "BCDFGHJK", true},
		{"BCDF.GHJK", "BCDFGHJK", true},
		{"ＢＣＤＦ－ＧＨＪＫ", "BCDFGHJK", true},      // full-width letters and hyphen
		{"ｂｃｄｆ\u3000ｇｈｊｋ", "BCDFGHJK", true}, // full-width lower case, ideographic space
		{"BCDF—GHJK", "BCDFGHJK", true},      // em dash
		{"BCDF−GHJK", "BCDFGHJK", true},      // minus sign
		{"BCDFーGHJK", "BCDFGHJK", true},      // katakana long vowel mark
		{"BCDFGHJ", "", false},               // too short
		{"BCDFGHJKL", "", false},             // too long
		{"BCDFGHJA", "", false},              // vowel outside the charset
		{"BCDFGHJ0", "", false},              // digit outside the charset
		{"BCDFGHJé", "", false},
		{"", "", false},
		{strings.Repeat("-", 300) + "BCDFGHJK", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeDeviceUserCode(tc.raw, testUserCodeCharset, 8)
		if got != tc.want || ok != tc.ok {
			t.Errorf("normalizeDeviceUserCode(%q) = %q, %v; want %q, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := normalizeDeviceUserCode("BCDFGHJK", "", 8); ok {
		t.Error("an empty charset must reject everything")
	}
	if _, ok := normalizeDeviceUserCode("BCDFGHJK", testUserCodeCharset, 0); ok {
		t.Error("a zero length must reject everything")
	}
}

func TestFormatDeviceUserCode(t *testing.T) {
	for raw, want := range map[string]string{"BCDFGHJK": "BCDF-GHJK", "BCDFGH": "BCDF-GH", "BCDFGHJKLMNP": "BCDF-GHJK-LMNP"} {
		if got := formatDeviceUserCode(raw); got != want {
			t.Errorf("formatDeviceUserCode(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSanitizeDeviceLabel(t *testing.T) {
	cases := map[string]string{
		"  Haruki-Client @ home  ":              "Haruki-Client @ home",
		"evil\u202egnp.exe":                     "evilgnp.exe",
		"zero\u200bwidth\u2066isolate\u2069":    "zerowidthisolate",
		"tab\tand\nnewline":                     "tabandnewline",
		"many    spaces\u3000here":              "many spaces here",
		"\u0000\u0007":                          "",
		strings.Repeat("あ", 70):                 strings.Repeat("あ", 64),
		strings.Repeat("a", 63) + " " + "bcdef": strings.Repeat("a", 63),
	}
	for raw, want := range cases {
		if got := sanitizeDeviceLabel(raw); got != want {
			t.Errorf("sanitizeDeviceLabel(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestWrappedDeviceCode(t *testing.T) {
	code, raw := newWrappedDeviceCode()
	if !strings.HasPrefix(code, "hdc_") || len(code) != 47 || len(raw) != 32 {
		t.Fatalf("wrapped code %q has the wrong shape", code)
	}
	parsed, ok := parseWrappedDeviceCode(code)
	if !ok || string(parsed) != string(raw) {
		t.Fatal("parseWrappedDeviceCode did not round-trip")
	}
	for _, bad := range []string{"", "hdc_", code[:46], code + "A", "HDC_" + code[4:], "ory_dc_" + code[4:], "hdc_" + strings.Repeat("!", 43)} {
		if _, ok := parseWrappedDeviceCode(bad); ok {
			t.Errorf("parseWrappedDeviceCode accepted %q", bad)
		}
	}
	other, _ := newWrappedDeviceCode()
	if other == code {
		t.Fatal("two wrapped codes collided")
	}
	if flowID := newDeviceFlowID(); len(flowID) != 32 || strings.ToLower(flowID) != flowID {
		t.Fatalf("flow ID %q is not 32 lowercase hex characters", flowID)
	}
}

func TestSealHydraDeviceCodeRoundTrip(t *testing.T) {
	const hydraCode = "ory_dc_examplehydradevicecode.signature"
	_, raw := newWrappedDeviceCode()
	flowID := newDeviceFlowID()
	sealed, err := sealHydraDeviceCode(raw, flowID, testPublicClientID, hydraCode)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "ory_dc_") {
		t.Fatal("the sealed value contains the Hydra code")
	}
	opened, err := openHydraDeviceCode(raw, flowID, testPublicClientID, sealed)
	if err != nil || opened != hydraCode {
		t.Fatalf("openHydraDeviceCode = %q, %v", opened, err)
	}
	again, _ := sealHydraDeviceCode(raw, flowID, testPublicClientID, hydraCode)
	if again == sealed {
		t.Fatal("sealing must use a fresh nonce")
	}

	_, otherRaw := newWrappedDeviceCode()
	tampered := []byte(sealed)
	tampered[len(tampered)-1] ^= 1
	for name, open := range map[string]func() (string, error){
		"other wrapped code": func() (string, error) { return openHydraDeviceCode(otherRaw, flowID, testPublicClientID, sealed) },
		"other flow":         func() (string, error) { return openHydraDeviceCode(raw, newDeviceFlowID(), testPublicClientID, sealed) },
		"other client":       func() (string, error) { return openHydraDeviceCode(raw, flowID, "other-client", sealed) },
		"tampered":           func() (string, error) { return openHydraDeviceCode(raw, flowID, testPublicClientID, string(tampered)) },
		"truncated":          func() (string, error) { return openHydraDeviceCode(raw, flowID, testPublicClientID, sealed[:10]) },
		"not base64":         func() (string, error) { return openHydraDeviceCode(raw, flowID, testPublicClientID, "!!!") },
	} {
		if _, err := open(); err == nil {
			t.Errorf("%s: openHydraDeviceCode succeeded", name)
		}
	}
}
