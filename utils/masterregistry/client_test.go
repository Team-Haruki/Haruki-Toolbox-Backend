package masterregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func registry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		switch r.URL.Path {
		case "/v1/master/jp/current":
			_, _ = w.Write([]byte(`{"contentHash":"h1","dataVersion":"7.1.0.60"}`))
		case "/v1/master/en/current":
			_, _ = w.Write([]byte(`{"dataVersion":"6.1.1.10"}`))
		case "/v1/master/kr/current":
			_, _ = w.Write([]byte(`{}`))
		case "/v1/master/cn/current":
			_, _ = w.Write([]byte(`not json`))
		case "/v1/master/jp/files/costume3ds.json":
			_, _ = w.Write([]byte(`[{"id":` + r.URL.Query().Get("version") + `}]`))
		case "/v1/master/jp/files/big.json":
			_, _ = w.Write([]byte(strings.Repeat("x", 33)))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestContentHashPrefersTheHashThenTheDataVersion(t *testing.T) {
	srv := registry(t)
	defer srv.Close()
	c := New(srv.URL+"/", "test-agent", srv.Client())
	ctx := context.Background()
	for region, want := range map[string]string{"jp": "h1", "en": "6.1.1.10"} {
		if got, err := c.ContentHash(ctx, region); err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", region, got, err, want)
		}
	}
	for _, region := range []string{"kr", "cn", "tw"} {
		if _, err := c.ContentHash(ctx, region); err == nil {
			t.Errorf("%s: want an error", region)
		}
	}
}

func TestFileReadsOneVersionAndRejectsWhatIsTooLarge(t *testing.T) {
	srv := registry(t)
	defer srv.Close()
	c := New(srv.URL, "test-agent", srv.Client())
	ctx := context.Background()
	if body, err := c.File(ctx, "jp", "costume3ds", "42"); err != nil || string(body) != `[{"id":42}]` {
		t.Fatalf("file: %q, %v", body, err)
	}
	if _, err := c.File(ctx, "jp", "missing", "1"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("missing file: %v", err)
	}
	previous := maxFileBytes
	maxFileBytes = 32
	defer func() { maxFileBytes = previous }()
	if _, err := c.File(ctx, "jp", "big", "1"); err == nil || !strings.Contains(err.Error(), "more than 32 bytes") {
		t.Fatalf("oversized file: %v", err)
	}
}
