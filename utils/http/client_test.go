package http

import (
	"context"
	"io"
	stdhttp "net/http"
	"strings"
	"testing"
	"time"
)

func TestNewClientUsesDefaultTimeout(t *testing.T) {
	t.Parallel()

	c := NewClient("", 0)
	if c.client == nil {
		t.Fatalf("client should be initialized")
	}
	if got := c.client.GetClient().Timeout; got != defaultRequestTimeout {
		t.Fatalf("default timeout = %v, want %v", got, defaultRequestTimeout)
	}
}

func TestNewClientUsesCustomTimeout(t *testing.T) {
	t.Parallel()

	c := NewClient("", 5*time.Second)
	if got := c.client.GetClient().Timeout; got != 5*time.Second {
		t.Fatalf("custom timeout = %v, want 5s", got)
	}
}

func TestRequestSuccess(t *testing.T) {
	t.Parallel()

	rt := &mockRoundTripper{t: t}
	c := NewClient("", 0)
	c.client.SetTransport(rt)

	status, headers, body, err := c.Request(
		context.Background(),
		stdhttp.MethodPost,
		"https://example.com/test",
		map[string]string{"Authorization": "Bearer token"},
		[]byte("abc"),
	)
	if err != nil {
		t.Fatalf("Request returned error: %v", err)
	}
	if status != stdhttp.StatusCreated {
		t.Fatalf("status = %d, want %d", status, stdhttp.StatusCreated)
	}
	if headers["X-Test"] != "ok" {
		t.Fatalf("response header X-Test = %q, want %q", headers["X-Test"], "ok")
	}
	if string(body) != "done" {
		t.Fatalf("response body = %q, want %q", string(body), "done")
	}
}

func TestFlattenHeaders(t *testing.T) {
	t.Parallel()

	in := map[string][]string{
		"X-A": {"1", "2"},
		"X-B": {},
	}
	out := flattenHeaders(in)

	if out["X-A"] != "1" {
		t.Fatalf("X-A = %q, want %q", out["X-A"], "1")
	}
	if _, ok := out["X-B"]; ok {
		t.Fatalf("X-B should not exist for empty values")
	}
}

type mockRoundTripper struct {
	t *testing.T
}

func (m *mockRoundTripper) RoundTrip(r *stdhttp.Request) (*stdhttp.Response, error) {
	m.t.Helper()

	if r.Method != stdhttp.MethodPost {
		m.t.Fatalf("method = %s, want POST", r.Method)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer token" {
		m.t.Fatalf("Authorization header = %q", got)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		m.t.Fatalf("read body error: %v", err)
	}
	if string(body) != "abc" {
		m.t.Fatalf("request body = %q, want %q", string(body), "abc")
	}

	return &stdhttp.Response{
		StatusCode: stdhttp.StatusCreated,
		Header:     stdhttp.Header{"X-Test": []string{"ok"}},
		Body:       io.NopCloser(strings.NewReader("done")),
	}, nil
}
