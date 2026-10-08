package redact

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var fingerprintShape = regexp.MustCompile(`^<redacted:[0-9a-f]{8}>$`)

func TestFingerprintIsShortStableAndDistinct(t *testing.T) {
	t.Parallel()
	a := Fingerprint("28808221489823746")
	if !fingerprintShape.MatchString(a) {
		t.Fatalf("Fingerprint = %q, want <redacted:8 hex>", a)
	}
	if strings.Contains(a, "28808221489823746") || strings.Contains(a, "3746") {
		t.Fatalf("fingerprint carries the id: %q", a)
	}
	if again := Fingerprint("28808221489823746"); again != a {
		t.Fatalf("same id fingerprinted twice: %q then %q", a, again)
	}
	if b := Fingerprint("28808221489823747"); b == a {
		t.Fatalf("neighbouring ids share a fingerprint: %q", a)
	}
}

// The key is random per process, so an unkeyed hash of the id must not be
// what comes out: otherwise hashing candidate ids would reverse it.
func TestFingerprintIsKeyed(t *testing.T) {
	t.Parallel()
	if len(fingerprintKey) != 32 {
		t.Fatalf("fingerprint key is %d bytes", len(fingerprintKey))
	}
	other := newFingerprintKey()
	if string(other) == string(fingerprintKey) {
		t.Fatal("two generated keys are equal")
	}
}

func TestMaskIDs(t *testing.T) {
	t.Parallel()
	const id = "99887766554433221"
	in := "GET /api/user/" + id + "/home/refresh and /suite/user/" + id + "?isLogin=true"
	got := MaskIDs(in, id, "", "123")
	if strings.Contains(got, id) {
		t.Fatalf("MaskIDs left the id: %q", got)
	}
	fp := Fingerprint(id)
	want := "GET /api/user/" + fp + "/home/refresh and /suite/user/" + fp + "?isLogin=true"
	if got != want {
		t.Fatalf("MaskIDs\n got  %q\n want %q", got, want)
	}
	// Short ids could match unrelated text and are left alone.
	if got := MaskIDs("status 12345", "12345"); got != "status 12345" {
		t.Fatalf("short id was masked: %q", got)
	}
	if got := MaskIDs("nothing here", id); got != "nothing here" {
		t.Fatalf("text without the id changed: %q", got)
	}
}

func TestIDsInErrorRewritesURLErrorAndKeepsTheChain(t *testing.T) {
	t.Parallel()
	const id = "99887766554433221"
	base := &url.Error{Op: "Get", URL: "https://game.example/api/suite/user/" + id, Err: errors.New("EOF")}
	wrapped := fmt.Errorf("call failed: %w", base)

	got := IDsInError(wrapped, id)
	if strings.Contains(got.Error(), id) {
		t.Fatalf("error kept the id: %q", got.Error())
	}
	var urlErr *url.Error
	if !errors.As(got, &urlErr) {
		t.Fatal("*url.Error must stay reachable")
	}
	if strings.Contains(urlErr.URL, id) {
		t.Fatalf("*url.Error URL kept the id: %q", urlErr.URL)
	}
	if !errors.Is(got, base) {
		t.Fatal("errors.Is must still find the original error")
	}

	if IDsInError(nil, id) != nil {
		t.Fatal("nil error must stay nil")
	}
	plain := errors.New("no ids here")
	if IDsInError(plain, id) != plain {
		t.Fatal("an error without the id must be returned as is")
	}
}

// A fingerprint survives a second pass through Text unchanged, so the
// logger's own redaction cannot mangle it.
func TestFingerprintSurvivesText(t *testing.T) {
	t.Parallel()
	line := "API returned non-200 status for https://game.example/api/user/" + Fingerprint("99887766554433221") + "/mysekai?isForceAllReloadOnlyMySekai=True: 500"
	if got := Text(line); got != line {
		t.Fatalf("Text changed a fingerprinted line:\n got  %q\n want %q", got, line)
	}
}
