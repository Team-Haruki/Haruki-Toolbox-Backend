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

	in := "https://h.example/cb?state=ok&code=abc123&access_token=tok&Password=pw&inherit_id=id1&inheritPassword=p2&key=upload_time&client_secret=cs"
	want := "https://h.example/cb?state=ok&code=<redacted>&access_token=<redacted>&Password=<redacted>&inherit_id=<redacted>&inheritPassword=<redacted>&key=upload_time&client_secret=<redacted>"
	if got := Text(in); got != want {
		t.Fatalf("Text()\n got  %q\n want %q", got, want)
	}
}

func TestTextRedactsSensitiveJSONFields(t *testing.T) {
	t.Parallel()

	in := `{"inherit_id":"id\"1","inherit_password":"pw","server":"jp"}`
	want := `{"inherit_id":"<redacted>","inherit_password":"<redacted>","server":"jp"}`
	if got := Text(in); got != want {
		t.Fatalf("Text()\n got  %q\n want %q", got, want)
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
