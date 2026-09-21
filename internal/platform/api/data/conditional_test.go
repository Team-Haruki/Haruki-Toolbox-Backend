package data

import "testing"

func TestNotModifiedDecision(t *testing.T) {
	const now = int64(1752600100)
	cases := []struct {
		name    string
		known   int64
		current int64
		found   bool
		want    bool
	}{
		{name: "match with elapsed second", known: 1752600000, current: 1752600000, found: true, want: true},
		{name: "match but stamp second still open", known: now, current: now, found: true, want: false},
		{name: "match but stamp in the future", known: now + 5, current: now + 5, found: true, want: false},
		{name: "client stamp older", known: 1752590000, current: 1752600000, found: true, want: false},
		{name: "client stamp newer", known: 1752600001, current: 1752600000, found: true, want: false},
		{name: "document missing", known: 1752600000, current: 0, found: false, want: false},
		{name: "document without stamp", known: 1752600000, current: 0, found: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notModifiedDecision(tc.known, tc.current, now, tc.found); got != tc.want {
				t.Fatalf("notModifiedDecision(%d, %d, %d, %v) = %v, want %v", tc.known, tc.current, now, tc.found, got, tc.want)
			}
		})
	}
}

func TestRequestKeyIncludesUploadTime(t *testing.T) {
	cases := []struct {
		name       string
		requestKey string
		want       bool
	}{
		{name: "single upload_time", requestKey: "upload_time", want: true},
		{name: "among other keys", requestKey: "userCards,upload_time", want: true},
		{name: "missing", requestKey: "userCards,userGamedata", want: false},
		{name: "padded segment does not match", requestKey: "userCards, upload_time", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestKeyIncludesUploadTime(tc.requestKey); got != tc.want {
				t.Fatalf("requestKeyIncludesUploadTime(%q) = %v, want %v", tc.requestKey, got, tc.want)
			}
		})
	}
}

func TestInvalidSuiteRequestKey(t *testing.T) {
	allowed := map[string]struct{}{"upload_time": {}, "userCards": {}}
	cases := []struct {
		name        string
		requestKey  string
		wantKey     string
		wantInvalid bool
	}{
		{name: "empty filter", requestKey: ""},
		{name: "allowed keys", requestKey: "upload_time,userCards"},
		{name: "userGamedata is exempt", requestKey: "userGamedata,upload_time"},
		{name: "disallowed key", requestKey: "userCards,secret", wantKey: "secret", wantInvalid: true},
		{name: "empty segment", requestKey: "userCards,,upload_time", wantKey: "", wantInvalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, invalid := InvalidSuiteRequestKey(tc.requestKey, allowed)
			if key != tc.wantKey || invalid != tc.wantInvalid {
				t.Fatalf("InvalidSuiteRequestKey(%q) = (%q, %v), want (%q, %v)", tc.requestKey, key, invalid, tc.wantKey, tc.wantInvalid)
			}
		})
	}
}

func TestInvalidMysekaiRequestKey(t *testing.T) {
	cases := []struct {
		name        string
		requestKey  string
		wantKey     string
		wantInvalid bool
	}{
		{name: "empty filter", requestKey: ""},
		{name: "plain keys", requestKey: "updatedResources,upload_time"},
		{name: "dotted key", requestKey: "updatedResources.userMysekaiPhotos", wantKey: "updatedResources.userMysekaiPhotos", wantInvalid: true},
		{name: "operator key", requestKey: "$where", wantKey: "$where", wantInvalid: true},
		{name: "blank segment", requestKey: "updatedResources, ,upload_time", wantKey: " ", wantInvalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, invalid := InvalidMysekaiRequestKey(tc.requestKey)
			if key != tc.wantKey || invalid != tc.wantInvalid {
				t.Fatalf("InvalidMysekaiRequestKey(%q) = (%q, %v), want (%q, %v)", tc.requestKey, key, invalid, tc.wantKey, tc.wantInvalid)
			}
		})
	}
}

func TestParseKnownUploadTime(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "absent", raw: "", want: 0},
		{name: "valid", raw: "1752600000", want: 1752600000},
		{name: "surrounding whitespace", raw: " 1752600000 ", want: 1752600000},
		{name: "zero is ignored", raw: "0", want: 0},
		{name: "negative is ignored", raw: "-5", want: 0},
		{name: "non-numeric is ignored", raw: "latest", want: 0},
		{name: "float is ignored", raw: "1752600000.5", want: 0},
		{name: "overflow is ignored", raw: "99999999999999999999", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseKnownUploadTime(tc.raw); got != tc.want {
				t.Fatalf("ParseKnownUploadTime(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
