package sekai

import (
	"bytes"
	"errors"
	"haruki-suite/config"
	harukiUtils "haruki-suite/utils"
	"testing"
)

const (
	testAESKeyHex = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testAESIVHex  = "0102030405060708090a0b0c0d0e0f10"
)

func mustTestCryptor(t *testing.T) *SekaiCryptor {
	t.Helper()

	c, err := NewSekaiCryptorFromHex(testAESKeyHex, testAESIVHex)
	if err != nil {
		t.Fatalf("NewSekaiCryptorFromHex failed: %v", err)
	}
	return c
}

func TestNewSekaiCryptorFromHex_InvalidInput(t *testing.T) {
	if _, err := NewSekaiCryptorFromHex("bad", testAESIVHex); err == nil {
		t.Fatalf("expected invalid aes key hex error")
	}
	if _, err := NewSekaiCryptorFromHex(testAESKeyHex, "abcd"); err == nil {
		t.Fatalf("expected invalid iv length error")
	}
}

func TestSekaiCryptorPackUnpack_RoundTripMap(t *testing.T) {
	c := mustTestCryptor(t)

	input := map[string]any{
		"a": "1",
		"n": int64(2),
		"nested": map[string]any{
			"k": "v",
		},
		"arr": []any{
			int64(1),
			map[string]any{"x": "y"},
		},
	}

	encrypted, err := c.Pack(input)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}
	if len(encrypted) == 0 {
		t.Fatalf("Pack returned empty payload")
	}

	unpackedAny, err := c.Unpack(encrypted)
	if err != nil {
		t.Fatalf("Unpack failed: %v", err)
	}
	unpacked, ok := unpackedAny.(map[string]any)
	if !ok {
		t.Fatalf("Unpack type = %T, want map[string]any", unpackedAny)
	}

	if unpacked["a"] != "1" {
		t.Fatalf("unpacked[a] = %v, want 1", unpacked["a"])
	}
	nested, ok := unpacked["nested"].(map[string]any)
	if !ok {
		t.Fatalf("unpacked[nested] type = %T", unpacked["nested"])
	}
	if nested["k"] != "v" {
		t.Fatalf("unpacked nested value = %v, want v", nested["k"])
	}
}

func TestSekaiCryptorPack_Validation(t *testing.T) {
	c := mustTestCryptor(t)

	if _, err := c.Pack(nil); !errors.Is(err, ErrNilContent) {
		t.Fatalf("Pack(nil) err = %v, want ErrNilContent", err)
	}
	if _, err := c.Pack([]byte{}); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("Pack(empty) err = %v, want ErrEmptyContent", err)
	}
}

func TestSekaiCryptorUnpackInto_Validation(t *testing.T) {
	c := mustTestCryptor(t)

	if err := c.UnpackInto(nil, &map[string]any{}); !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("UnpackInto(nil) err = %v, want ErrEmptyContent", err)
	}
	if err := c.UnpackInto([]byte{1, 2, 3}, &map[string]any{}); !errors.Is(err, ErrInvalidBlockSize) {
		t.Fatalf("UnpackInto invalid block err = %v, want ErrInvalidBlockSize", err)
	}
	if err := c.UnpackInto(make([]byte, 16), nil); err == nil {
		t.Fatalf("UnpackInto with nil out should fail")
	}
}

func TestSafePKCS7Unpad(t *testing.T) {
	valid := bytes.Repeat([]byte{16}, 16)
	if _, err := safePKCS7Unpad(valid); err != nil {
		t.Fatalf("safePKCS7Unpad(valid) err = %v", err)
	}

	if _, err := safePKCS7Unpad([]byte{1, 2, 3}); !errors.Is(err, ErrInvalidBlockSize) {
		t.Fatalf("safePKCS7Unpad(invalid block) err = %v, want ErrInvalidBlockSize", err)
	}

	invalidPadding := append(make([]byte, 15), 2)
	if _, err := safePKCS7Unpad(invalidPadding); err == nil {
		t.Fatalf("safePKCS7Unpad should fail for invalid padding bytes")
	}
}

func TestConvertUnpackResult_MapAnyAny(t *testing.T) {
	in := map[any]any{
		"a": 1,
		123: "ignored",
	}
	out, ok := convertUnpackResult(in).(map[string]any)
	if !ok {
		t.Fatalf("convertUnpackResult type = %T, want map[string]any", out)
	}
	if out["a"] != 1 {
		t.Fatalf("out[a] = %v, want 1", out["a"])
	}
	if _, exists := out["123"]; exists {
		t.Fatalf("numeric key should not be converted")
	}
}

func TestUnpackOrdered(t *testing.T) {
	c := mustTestCryptor(t)

	rawMsgpack := []byte{0x81, 0xa1, 'k', 0xa1, 'v'}
	encrypted, err := c.Pack(rawMsgpack)
	if err != nil {
		t.Fatalf("Pack failed: %v", err)
	}
	out, err := c.UnpackOrdered(encrypted)
	if err != nil {
		t.Fatalf("UnpackOrdered failed: %v", err)
	}
	if v, _ := out.Get("k"); v != "v" {
		t.Fatalf("UnpackOrdered value = %v, want v", v)
	}
}

func TestDecryptToMsgpack(t *testing.T) {
	originalCfg := config.Cfg
	t.Cleanup(func() {
		config.Cfg = originalCfg
	})

	config.Cfg.SekaiClient.ENServerAESKey = testAESKeyHex
	config.Cfg.SekaiClient.ENServerAESIV = testAESIVHex
	config.Cfg.SekaiClient.OtherServerAESKey = testAESKeyHex
	config.Cfg.SekaiClient.OtherServerAESIV = testAESIVHex

	cryptor := mustTestCryptor(t)
	rawMsgpack := []byte{0x81, 0xa1, 'a', 0x01}
	encrypted, err := cryptor.Pack(rawMsgpack)
	if err != nil {
		t.Fatalf("Pack raw msgpack failed: %v", err)
	}

	decrypted, err := DecryptToMsgpack(encrypted, harukiUtils.SupportedDataUploadServerJP)
	if err != nil {
		t.Fatalf("DecryptToMsgpack failed: %v", err)
	}
	if !bytes.Equal(decrypted, rawMsgpack) {
		t.Fatalf("DecryptToMsgpack mismatch: got %x want %x", decrypted, rawMsgpack)
	}
}
