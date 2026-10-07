package redact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const fakeInheritID = "FAKEinheritID0001"

func TestTextRedactsSecretPathSegments(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "game API inherit URL keeps its query",
			in:   "https://game.example/api/inherit/user/" + fakeInheritID + "?isExecuteInherit=False&isAdult=True&tAge=16",
			want: "https://game.example/api/inherit/user/<redacted>?isExecuteInherit=False&isAdult=True&tAge=16",
		},
		{
			name: "APIError message",
			in:   "sekai API error: POST /inherit/user/" + fakeInheritID + "?isExecuteInherit=False returned status 403: non-200 response",
			want: "sekai API error: POST /inherit/user/<redacted>?isExecuteInherit=False returned status 403: non-200 response",
		},
		{
			name: "quoted URL inside a transport error",
			in:   `Post "https://game.example/api/inherit/user/` + fakeInheritID + `?isExecuteInherit=False": EOF`,
			want: `Post "https://game.example/api/inherit/user/<redacted>?isExecuteInherit=False": EOF`,
		},
		{
			name: "iOS upload script path",
			in:   "POST /ios/script/abcdef0123456789abcdef0123456789/upload (Sent: 12)",
			want: "POST /ios/script/<redacted>/upload (Sent: 12)",
		},
		{
			name: "iOS module path under /api",
			in:   "GET /api/ios/module/abcdef0123456789abcdef0123456789/haruki.sgmodule",
			want: "GET /api/ios/module/<redacted>/haruki.sgmodule",
		},
		{
			name: "Afdian callback secret",
			in:   "POST /api/sponsor/afdian/callback/s3cr3t-value (Sent: 12)",
			want: "POST /api/sponsor/afdian/callback/<redacted> (Sent: 12)",
		},
		{
			name: "non-secret iOS proxy path untouched",
			in:   "GET /ios/proxy/jp/suite/user/12345",
			want: "GET /ios/proxy/jp/suite/user/12345",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Text(tc.in); got != tc.want {
				t.Fatalf("Text(%q)\n got  %q\n want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTextRedactsSensitiveQueryParams(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "authorization code callback and other credentials",
			in:   "https://h.example/cb?state=ok&code=abc123&access_token=tok&Password=pw&inherit_id=id1&inheritPassword=p2&key=upload_time&client_secret=cs",
			want: "https://h.example/cb?state=ok&code=<redacted>&access_token=<redacted>&Password=<redacted>&inherit_id=<redacted>&inheritPassword=<redacted>&key=upload_time&client_secret=<redacted>",
		},
		{
			name: "device verification page user_code",
			in:   "GET /device?user_code=BCDFGHJK",
			want: "GET /device?user_code=<redacted>",
		},
		{
			name: "user code spellings",
			in:   "/api/oauth2/device/lookup?client_id=bot&user-code=BCDF-GHJK&usercode=MNPQRSTV&userCode=WXZBCDFG&state=keep",
			want: "/api/oauth2/device/lookup?client_id=bot&user-code=<redacted>&usercode=<redacted>&userCode=<redacted>&state=keep",
		},
		{
			name: "device_code in a token request form",
			in:   "grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=ory_dc_x.y&client_id=bot",
			want: "grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=<redacted>&client_id=bot",
		},
		{
			name: "device code spellings",
			in:   "/poll?device-code=abc&deviceCode=def&DEVICE_CODE=ghi&interval=5",
			want: "/poll?device-code=<redacted>&deviceCode=<redacted>&DEVICE_CODE=<redacted>&interval=5",
		},
		{
			name: "device_challenge keeps the challenge rule",
			in:   "/oauth2/device/verify?device_challenge=abc123&state=keep",
			want: "/oauth2/device/verify?device_challenge=<redacted>&state=keep",
		},
		{
			name: "verification_uri_complete inside JSON",
			in:   `{"verification_uri_complete":"https://toolbox.example/device?user_code=BCDF-GHJK","expires_in":600}`,
			want: `{"verification_uri_complete":"https://toolbox.example/device?user_code=<redacted>","expires_in":600}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Text(tc.in); got != tc.want {
				t.Fatalf("Text(%q)\n got  %q\n want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTextRedactsSensitiveJSONFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "inherit credentials",
			in:   `{"inherit_id":"id\"1","inherit_password":"pw","server":"jp"}`,
			want: `{"inherit_id":"<redacted>","inherit_password":"<redacted>","server":"jp"}`,
		},
		{
			name: "device flow browser body in camelCase",
			in:   `{"flowHandle":"dfh_abc","userCode":"BCDF-GHJK","deviceCode":"hdc_abc","label":"keep"}`,
			want: `{"flowHandle":"<redacted>","userCode":"<redacted>","deviceCode":"<redacted>","label":"keep"}`,
		},
		{
			name: "Hydra device authorization response in snake_case",
			in:   `{"device_code":"ory_dc_abc.def","user_code":"BCDFGHJK","expires_in":600,"interval":5}`,
			want: `{"device_code":"<redacted>","user_code":"<redacted>","expires_in":600,"interval":5}`,
		},
		{
			name: "flow_handle with spaces around the colon",
			in:   `{"flow_handle" : "dfh_abc", "state": "claimed"}`,
			want: `{"flow_handle" : "<redacted>", "state": "claimed"}`,
		},
		{
			name: "hyphenated user-code, device-code and flow-handle keys",
			in:   `{"user-code":"BCDFGHJK","device-code":"abc","flow-handle":"dfh_abc","label":"keep"}`,
			want: `{"user-code":"<redacted>","device-code":"<redacted>","flow-handle":"<redacted>","label":"keep"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Text(tc.in); got != tc.want {
				t.Fatalf("Text(%q)\n got  %q\n want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTextRedactsTokenLiterals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "Hydra access token in a bearer header",
			in:   "Authorization: Bearer ory_at_FAKEaccess0123456789.sig-part_x",
			want: "Authorization: Bearer <redacted>",
		},
		{
			name: "Hydra refresh token, authorization code and device code in free text",
			in:   "rt ory_rt_FAKErefresh0123456789 ac ory_ac_FAKEauthcode012345 dc ory_dc_FAKEdevicecode0123.sig",
			want: "rt <redacted> ac <redacted> dc <redacted>",
		},
		{
			name: "wrapped device code and flow handle",
			in:   "poll hdc_FAKEwrappedDeviceCode0123456789-_x for flow dfh_FAKEflowHandle0123456789",
			want: "poll <redacted> for flow <redacted>",
		},
		{
			// Holds no ory_ or hdc_, so only the dfh_ gate lets the regex run.
			name: "flow handle alone in free text",
			in:   "claim failed for dfh_FAKEflowHandle0123456789",
			want: "claim failed for <redacted>",
		},
		{
			name: "literal under a parameter name that is not a credential",
			in:   "/cb?next=hdc_FAKEwrapped0123456789&state=keep",
			want: "/cb?next=<redacted>&state=keep",
		},
		{
			name: "literal under a credential name is redacted once",
			in:   "/cb?access_token=ory_at_FAKEaccess0123456789&state=keep",
			want: "/cb?access_token=<redacted>&state=keep",
		},
		{
			name: "literal inside a JSON value of another field",
			in:   `{"error":"invalid_grant","hint":"ory_dc_FAKEdevicecode0123 expired"}`,
			want: `{"error":"invalid_grant","hint":"<redacted> expired"}`,
		},
		{
			name: "suffix of exactly 10 characters is redacted, 9 stays",
			in:   "hdc_0123456789 hdc_123456789",
			want: "<redacted> hdc_123456789",
		},
		{
			name: "short suffixes and other prefixes stay",
			in:   "hdc_x dfh_123456789 ory_at_short ory_xx_FAKEnotatoken0123",
			want: "hdc_x dfh_123456789 ory_at_short ory_xx_FAKEnotatoken0123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Text(tc.in); got != tc.want {
				t.Fatalf("Text(%q)\n got  %q\n want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTextRedactsExactSecretsIncludingEscapedForms(t *testing.T) {
	t.Parallel()

	// A value pasted with a space: pattern matching would stop at the space,
	// the exact value must not survive in raw or escaped form.
	secret := "FAKE ID 0001"
	for _, in := range []string{
		"/inherit/user/" + secret + "?isExecuteInherit=False",
		"https://game.example/api/inherit/user/" + url.PathEscape(secret) + "?isExecuteInherit=False",
		"payload id=" + url.QueryEscape(secret),
	} {
		got := Text(in, secret)
		if strings.Contains(got, "FAKE") || strings.Contains(got, "0001") {
			t.Fatalf("Text(%q) leaked the secret: %q", in, got)
		}
	}

	// Very short values are not used for exact replacement.
	if got := Text("status ok", "ok"); got != "status ok" {
		t.Fatalf("short secret rewrote unrelated text: %q", got)
	}
}

func TestErrorRedactsURLErrorAndKeepsChain(t *testing.T) {
	t.Parallel()

	// A server that drops the connection produces a real *url.Error from
	// net/http whose message repeats the full request URL.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("hijacking not supported")
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	srv.Start()
	defer srv.Close()

	requestURL := srv.URL + "/api/inherit/user/" + fakeInheritID + "?isExecuteInherit=False"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, requestURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	_, rawErr := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if rawErr == nil {
		t.Fatal("expected a transport error")
	}
	if !strings.Contains(rawErr.Error(), fakeInheritID) {
		t.Fatalf("precondition: *url.Error should contain the URL, got %q", rawErr.Error())
	}

	wrapped := fmt.Errorf("client initialization failed: %w", rawErr)
	got := Error(wrapped)
	if strings.Contains(got.Error(), fakeInheritID) {
		t.Fatalf("redacted error leaked inherit ID: %q", got.Error())
	}
	if !strings.Contains(got.Error(), "/inherit/user/<redacted>?isExecuteInherit=False") {
		t.Fatalf("redacted error lost context: %q", got.Error())
	}

	var urlErr *url.Error
	if !errors.As(got, &urlErr) {
		t.Fatal("errors.As(*url.Error) must still succeed after redaction")
	}
	if strings.Contains(urlErr.URL, fakeInheritID) || strings.Contains(urlErr.Error(), fakeInheritID) {
		t.Fatalf("unwrapped *url.Error still carries the inherit ID: %q", urlErr.Error())
	}
}

func TestErrorPreservesTimeoutClassification(t *testing.T) {
	t.Parallel()

	inner := &url.Error{
		Op:  "Post",
		URL: "https://game.example/api/inherit/user/" + fakeInheritID + "?isExecuteInherit=False",
		Err: &net.OpError{Op: "dial", Err: context.DeadlineExceeded},
	}
	got := Error(fmt.Errorf("wrap: %w", inner), fakeInheritID)
	if strings.Contains(got.Error(), fakeInheritID) {
		t.Fatalf("leaked: %q", got.Error())
	}
	if !errors.Is(got, context.DeadlineExceeded) {
		t.Fatal("errors.Is(context.DeadlineExceeded) must survive redaction")
	}
}

func TestErrorWrapsNonURLErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("POST /inherit/user/" + fakeInheritID + "?isExecuteInherit=False failed")
	got := Error(sentinel)
	if strings.Contains(got.Error(), fakeInheritID) {
		t.Fatalf("leaked: %q", got.Error())
	}
	if !errors.Is(got, sentinel) {
		t.Fatal("redacted error must unwrap to the original")
	}

	clean := errors.New("nothing secret here")
	if Error(clean) != clean {
		t.Fatal("an error without secrets should be returned unchanged")
	}
	if Error(nil) != nil {
		t.Fatal("Error(nil) should be nil")
	}
}

func TestWriterRedactsEachWrite(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	w := Writer(&buf)
	line := "[2026-09-30 00:00:00] 127.0.0.1 | 200 - 1ms POST /ios/script/abcdef0123456789/upload (Sent: 2)\n"
	n, err := w.Write([]byte(line))
	if err != nil || n != len(line) {
		t.Fatalf("Write returned n=%d err=%v, want n=%d", n, err, len(line))
	}
	if strings.Contains(buf.String(), "abcdef0123456789") {
		t.Fatalf("writer leaked upload code: %q", buf.String())
	}
	if !strings.HasSuffix(buf.String(), "/ios/script/<redacted>/upload (Sent: 2)\n") {
		t.Fatalf("unexpected writer output: %q", buf.String())
	}
}
