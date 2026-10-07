// Package botsecurity collects the bot security alerts Haruki Cloud raises
// (a bot or source crossing a failure threshold) and lets admins triage them.
// Cloud posts alerts to an internal endpoint on the backend port; admins read
// and update them under /api/admin/bot-security.
package botsecurity

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"

	platformAuthHeader "github.com/Team-Haruki/Haruki-Toolbox-Backend/internal/platform/authheader"
)

// IngestConfig is the immutable configuration of the ingest endpoint. Its
// zero value is disabled: the route is not registered.
type IngestConfig struct {
	tokenSHA256 []byte
}

// ParseIngestConfig validates bot_security.ingest_token_sha256. An empty value
// (after trimming) disables ingestion; otherwise it must be exactly 64 hex
// characters in either case. Errors name the key but never repeat its value.
func ParseIngestConfig(tokenSHA256 string) (IngestConfig, error) {
	value := strings.TrimSpace(tokenSHA256)
	if value == "" {
		return IngestConfig{}, nil
	}
	if len(value) != 2*sha256.Size {
		return IngestConfig{}, fmt.Errorf("bot_security.ingest_token_sha256 must be %d hex characters, got %d", 2*sha256.Size, len(value))
	}
	sum, err := hex.DecodeString(value)
	if err != nil {
		return IngestConfig{}, fmt.Errorf("bot_security.ingest_token_sha256 must be hex")
	}
	return IngestConfig{tokenSHA256: sum}, nil
}

// Enabled reports whether ingestion is configured.
func (c IngestConfig) Enabled() bool {
	return len(c.tokenSHA256) == sha256.Size
}

// authorize checks "Authorization: Bearer <token>" against the configured
// hash in constant time. Exactly one Authorization header is accepted. It
// returns "" on success, otherwise the logged reason: "missing", "multiple",
// "malformed" or "mismatch". No reason carries a credential.
func (c IngestConfig) authorize(headers [][]byte) string {
	switch len(headers) {
	case 0:
		return "missing"
	case 1:
	default:
		return "multiple"
	}
	header := string(headers[0])
	if strings.TrimSpace(header) == "" {
		return "missing"
	}
	token, ok := platformAuthHeader.ExtractBearerToken(header)
	if !ok {
		return "malformed"
	}
	sum := sha256.Sum256([]byte(token))
	if !c.Enabled() || subtle.ConstantTimeCompare(sum[:], c.tokenSHA256) != 1 {
		return "mismatch"
	}
	return ""
}
