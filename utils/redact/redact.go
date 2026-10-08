// Package redact strips credentials from text before it reaches a log line or
// an error string that may end up in one.
//
// Game inherit IDs travel in the game API path (/inherit/user/{id}), iOS upload
// codes and the Afdian callback secret travel in Toolbox route paths, OAuth2
// or webhook URLs can carry tokens in their query string, and OAuth2 device
// authorization user codes, device codes and flow handles travel in query
// strings and JSON bodies. Go's *url.Error repeats the full request URL, so
// any outbound failure would otherwise copy those values into the log.
//
// Identifiers that are not credentials but should still not be readable in a
// given log line, such as the game user ID an inherit resolves to, are masked
// with Fingerprint instead, which keeps repeats of one ID recognisable.
package redact

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// Placeholder replaces every redacted value.
const Placeholder = "<redacted>"

// minExactSecretLen keeps exact-value scrubbing from rewriting unrelated text
// when a caller passes a very short value.
const minExactSecretLen = 6

// secretPathSegment matches the path segment that follows a route prefix whose
// next segment is a credential:
//   - /inherit/user/{inherit_id}     game API account transfer
//   - /ios/script/{upload_code}/...  iOS upload script (also under /api/ios)
//   - /ios/module/{upload_code}/...  iOS module download (also under /api/ios)
//   - /afdian/callback/{secret}      Afdian sponsor webhook
var secretPathSegment = regexp.MustCompile(
	`(/(?:inherit/user|ios/script|ios/module|afdian/callback)/)[^/?#\s"'` + "`" + `<>]+`,
)

// secretQueryParam matches the value of a query or form parameter whose name
// denotes a credential: any name containing token/secret/password/credential/
// signature/verifier/assertion/challenge (access_token, id_token_hint,
// client_secret, code_verifier, login_challenge, device_challenge, ...) plus a
// few exact names, among them the OAuth2 device authorization user_code and
// device_code (also spelled with '-', without a separator, or in camelCase;
// the exact name `code` alone never matched them, since it must directly
// follow ?, & or ;). `key` alone is deliberately excluded: the private
// game-data API uses ?key= to select response fields.
var secretQueryParam = regexp.MustCompile(
	`(?i)([?&;](?:[a-z0-9_.-]*(?:token|secret|password|passwd|credential|signature|verifier|assertion|challenge)[a-z0-9_.-]*|inherit[_-]?id|pwd|code|user[_-]?code|device[_-]?code|sign|api[_-]?key|access[_-]?key)=)[^&#\s"'` + "`" + `<>]*`,
)

// secretJSONField matches a JSON string value whose key denotes a credential,
// including the OAuth2 device authorization user code, device code and flow
// handle in snake_case (Hydra) or camelCase (Toolbox browser bodies).
var secretJSONField = regexp.MustCompile(
	`(?i)("(?:inherit[_-]?id|inherit[_-]?password|password|credential|session[_-]?token|access[_-]?token|refresh[_-]?token|client[_-]?secret|user[_-]?code|device[_-]?code|flow[_-]?handle)"\s*:\s*")(?:[^"\\]|\\.)*"`,
)

// secretTokenLiteral matches a credential by its prefix wherever it appears,
// whatever parameter, field or header carries it: Hydra access tokens, refresh
// tokens, authorization codes and device codes (ory_at_, ory_rt_, ory_ac_,
// ory_dc_) and the OAuth2 device flow's wrapped device code (hdc_) and flow
// handle (dfh_). The 10-character minimum leaves short identifiers such as
// hdc_x alone. User codes carry no prefix, so only secretQueryParam and
// secretJSONField can catch them. A match needs no word boundary on its left,
// so an identifier that merely ends in a prefix is over-redacted from there on
// ("directory_at_0123456789" becomes "direct<redacted>"); that is the safe
// direction, and a boundary guard must not let concatenations such as
// "x_ory_at_..." through.
var secretTokenLiteral = regexp.MustCompile(
	`(?:ory_(?:at|rt|ac|dc)|hdc|dfh)_[A-Za-z0-9_\-.]{10,}`,
)

// Text returns s with credential-bearing path segments, query parameters, JSON
// fields and prefixed token literals replaced by Placeholder. Each value in
// secrets that is at least six characters long is also replaced wherever it
// appears verbatim or in its URL-escaped forms; callers that know the exact
// credential (for example the inherit ID a Sekai client was built with) should
// pass it, since pattern matching cannot know where a value containing spaces
// or '/' ends.
func Text(s string, secrets ...string) string {
	if s == "" {
		return s
	}
	for _, secret := range secrets {
		s = replaceSecret(s, secret)
	}
	if strings.Contains(s, "/inherit/user/") || strings.Contains(s, "/ios/") || strings.Contains(s, "/afdian/callback/") {
		s = secretPathSegment.ReplaceAllString(s, "${1}"+Placeholder)
	}
	if strings.IndexByte(s, '=') >= 0 {
		s = secretQueryParam.ReplaceAllString(s, "${1}"+Placeholder)
	}
	if strings.Contains(s, `":`) || strings.Contains(s, `" :`) {
		s = secretJSONField.ReplaceAllString(s, `${1}`+Placeholder+`"`)
	}
	// Runs after the name-based rules so a value they already replaced is left
	// alone; run first, it would make the query rule append a second
	// Placeholder after the one it left.
	if strings.Contains(s, "ory_") || strings.Contains(s, "hdc_") || strings.Contains(s, "dfh_") {
		s = secretTokenLiteral.ReplaceAllLiteralString(s, Placeholder)
	}
	return s
}

func replaceSecret(s, secret string) string {
	if len(secret) < minExactSecretLen {
		return s
	}
	s = strings.ReplaceAll(s, secret, Placeholder)
	if escaped := url.PathEscape(secret); escaped != secret {
		s = strings.ReplaceAll(s, escaped, Placeholder)
	}
	if escaped := url.QueryEscape(secret); escaped != secret {
		s = strings.ReplaceAll(s, escaped, Placeholder)
	}
	return s
}

// Error returns err with credentials removed from its message. A *url.Error
// anywhere in the chain has its URL field rewritten in place, so code that
// unwraps to it later still sees the redacted URL. When the message still
// changes after that, the result wraps err: Error() is redacted and Unwrap()
// returns err, so errors.Is / errors.As classification keeps working.
func Error(err error, secrets ...string) error {
	return rewriteError(err, func(s string) string { return Text(s, secrets...) })
}

// IDsInError is Error for MaskIDs: every id in err's message, and in the URL of
// a *url.Error in its chain, is replaced by its Fingerprint.
func IDsInError(err error, ids ...string) error {
	return rewriteError(err, func(s string) string { return MaskIDs(s, ids...) })
}

func rewriteError(err error, rewrite func(string) string) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr != nil {
		urlErr.URL = rewrite(urlErr.URL)
	}
	msg := err.Error()
	redacted := rewrite(msg)
	if redacted == msg {
		return err
	}
	return &redactedError{msg: redacted, err: err}
}

// fingerprintKey keys Fingerprint. It is random per process: identifiers such
// as game user IDs are structured enough to enumerate, so an unkeyed hash of
// one could be reversed by hashing candidates. The cost is that fingerprints
// only correlate within one process lifetime, which is what reading a single
// failing request across its log lines needs.
var fingerprintKey = newFingerprintKey()

func newFingerprintKey() []byte {
	key := make([]byte, 32)
	// crypto/rand.Read never returns an error on supported platforms (Go 1.24+
	// crashes the program instead), so the key is always random.
	_, _ = rand.Read(key)
	return key
}

// fingerprintHexLen is the number of hex digits a fingerprint keeps: 32 bits
// tell apart the handful of identifiers in flight at once, and are far too
// few to carry the identifier itself.
const fingerprintHexLen = 8

// Fingerprint returns a short, non-reversible stand-in for an identifier that
// should not appear in a log line but whose repeats are worth correlating:
// "<redacted:" + 8 hex digits + ">". The same id yields the same fingerprint
// for the life of the process.
func Fingerprint(id string) string {
	mac := hmac.New(sha256.New, fingerprintKey)
	_, _ = mac.Write([]byte(id))
	return "<redacted:" + hex.EncodeToString(mac.Sum(nil))[:fingerprintHexLen] + ">"
}

// MaskIDs replaces every verbatim occurrence of each id in s with its
// Fingerprint. Like the exact-value scrubbing in Text it ignores ids shorter
// than six characters, which could match unrelated text.
func MaskIDs(s string, ids ...string) string {
	for _, id := range ids {
		if len(id) < minExactSecretLen || !strings.Contains(s, id) {
			continue
		}
		s = strings.ReplaceAll(s, id, Fingerprint(id))
	}
	return s
}

type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.err }

// Writer returns an io.Writer that redacts each Write before passing it on. It
// is meant for line-oriented log sinks that receive one whole entry per Write,
// such as Fiber's access logger.
func Writer(w io.Writer) io.Writer {
	return &writer{w: w}
}

type writer struct {
	w io.Writer
}

func (rw *writer) Write(p []byte) (int, error) {
	redacted := Text(string(p))
	if _, err := io.WriteString(rw.w, redacted); err != nil {
		return 0, err
	}
	return len(p), nil
}
