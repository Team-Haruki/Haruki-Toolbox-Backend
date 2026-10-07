package oauth2

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// deviceWrappedCodePrefix marks the device code handed to devices. Hydra's
	// own ory_dc_ code never leaves the backend: it is sealed in Redis under a
	// key derived from the wrapped code, so only its holder can poll with it.
	deviceWrappedCodePrefix = "hdc_"
	deviceSecretBytes       = 32
	deviceFlowIDBytes       = 16
	deviceCodeWrapInfo      = "haruki/oauth2-device/dc-wrap/v1"
	deviceCodeWrapAADPrefix = "v1|"
	deviceLabelMaxRunes     = 64
	// deviceUserCodeMaxInputBytes bounds normalization work on hostile input;
	// a real code with separators and full-width forms is far shorter.
	deviceUserCodeMaxInputBytes = 256
)

var errDeviceCodeSeal = errors.New("device code seal is invalid")

// newDeviceFlowID returns 32 lowercase hex characters (16 random bytes).
func newDeviceFlowID() string {
	buf := make([]byte, deviceFlowIDBytes)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// newWrappedDeviceCode returns hdc_ + base64url-nopad(32 random bytes) and the
// raw bytes, which are the key material sealing Hydra's device code.
func newWrappedDeviceCode() (string, []byte) {
	raw := make([]byte, deviceSecretBytes)
	_, _ = rand.Read(raw)
	return deviceWrappedCodePrefix + base64.RawURLEncoding.EncodeToString(raw), raw
}

// parseWrappedDeviceCode accepts exactly hdc_ + the canonical encoding of 32
// bytes and returns those bytes.
func parseWrappedDeviceCode(code string) ([]byte, bool) {
	encoded, ok := strings.CutPrefix(code, deviceWrappedCodePrefix)
	if !ok || len(encoded) != base64.RawURLEncoding.EncodedLen(deviceSecretBytes) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(raw) != deviceSecretBytes {
		return nil, false
	}
	return raw, true
}

func deviceCodeSealAEAD(raw []byte, flowID string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, raw, []byte(flowID), deviceCodeWrapInfo, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func deviceCodeSealAAD(flowID, clientID string) []byte {
	return []byte(deviceCodeWrapAADPrefix + flowID + "|" + clientID)
}

// sealHydraDeviceCode encrypts Hydra's device code with AES-256-GCM under
// HKDF-SHA256(secret=raw, salt=fid, info=deviceCodeWrapInfo), binding it to
// the flow and client through the AAD. The result is
// base64url-nopad(nonce ‖ ciphertext ‖ tag).
func sealHydraDeviceCode(raw []byte, flowID, clientID, hydraDeviceCode string) (string, error) {
	aead, err := deviceCodeSealAEAD(raw, flowID)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce)
	sealed := aead.Seal(nonce, nonce, []byte(hydraDeviceCode), deviceCodeSealAAD(flowID, clientID))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openHydraDeviceCode reverses sealHydraDeviceCode.
func openHydraDeviceCode(raw []byte, flowID, clientID, sealed string) (string, error) {
	payload, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", errDeviceCodeSeal
	}
	aead, err := deviceCodeSealAEAD(raw, flowID)
	if err != nil {
		return "", err
	}
	if len(payload) < aead.NonceSize()+aead.Overhead() {
		return "", errDeviceCodeSeal
	}
	nonce, ciphertext := payload[:aead.NonceSize()], payload[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ciphertext, deviceCodeSealAAD(flowID, clientID))
	if err != nil {
		return "", errDeviceCodeSeal
	}
	return string(plain), nil
}

// normalizeDeviceUserCode maps what a person types to the code Hydra issued:
// full-width forms to ASCII, ASCII to upper case, and spaces, hyphens,
// underscores, dots and dash-like characters removed. The result must have
// exactly length runes, all from charset.
func normalizeDeviceUserCode(raw, charset string, length int) (string, bool) {
	if length <= 0 || charset == "" || len(raw) > deviceUserCodeMaxInputBytes {
		return "", false
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			r -= 0xFEE0
		case r == 0x3000:
			r = ' '
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		if unicode.IsSpace(r) || isDeviceUserCodeSeparator(r) {
			continue
		}
		b.WriteRune(r)
	}
	normalized := b.String()
	if utf8.RuneCountInString(normalized) != length {
		return "", false
	}
	for _, r := range normalized {
		if !strings.ContainsRune(charset, r) {
			return "", false
		}
	}
	return normalized, true
}

func isDeviceUserCodeSeparator(r rune) bool {
	switch {
	case r == '-', r == '_', r == '.', r == 0x2212, r == 0x30FC:
		return true
	case r >= 0x2010 && r <= 0x2015:
		return true
	}
	return false
}

// formatDeviceUserCode groups a normalized code in fours: BCDF-GHJK.
func formatDeviceUserCode(code string) string {
	runes := []rune(code)
	var b strings.Builder
	for i, r := range runes {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeDeviceLabel cleans the untrusted label a device reports about
// itself: control and format characters (bidi overrides, zero-width
// characters) are removed, whitespace runs collapse to one space, and the
// result is trimmed to deviceLabelMaxRunes runes.
func sanitizeDeviceLabel(raw string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range raw {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if unicode.IsSpace(r) {
			pendingSpace = b.Len() > 0
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	label := []rune(b.String())
	if len(label) > deviceLabelMaxRunes {
		label = label[:deviceLabelMaxRunes]
	}
	return strings.TrimSpace(string(label))
}
