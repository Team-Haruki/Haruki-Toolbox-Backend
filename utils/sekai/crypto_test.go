package sekai

import (
	"bytes"
	"errors"
	harukiUtils "github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"
	"strings"
	"testing"
)

const (
	testAESKeyHex  = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	testAESIVHex   = "0102030405060708090a0b0c0d0e0f10"
	testAESKeyHex2 = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"
	testAESIVHex2  = "100f0e0d0c0b0a090807060504030201"
)

func testServerCryptor() ServerCryptor {
	return NewServerCryptor(ServerCryptorConfig{
		ENServerAESKey:    testAESKeyHex,
		ENServerAESIV:     testAESIVHex,
		OtherServerAESKey: testAESKeyHex,
		OtherServerAESIV:  testAESIVHex,
	})
}

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
	cryptor := mustTestCryptor(t)
	rawMsgpack := []byte{0x81, 0xa1, 'a', 0x01}
	encrypted, err := cryptor.Pack(rawMsgpack)
	if err != nil {
		t.Fatalf("Pack raw msgpack failed: %v", err)
	}

	decrypted, err := testServerCryptor().DecryptToMsgpack(encrypted, harukiUtils.SupportedDataUploadServerJP)
	if err != nil {
		t.Fatalf("DecryptToMsgpack failed: %v", err)
	}
	if !bytes.Equal(decrypted, rawMsgpack) {
		t.Fatalf("DecryptToMsgpack mismatch: got %x want %x", decrypted, rawMsgpack)
	}
}

func TestServerCryptorSelectsENAndOtherServerMaterial(t *testing.T) {
	serverCryptor := NewServerCryptor(ServerCryptorConfig{
		ENServerAESKey:    testAESKeyHex,
		ENServerAESIV:     testAESIVHex,
		OtherServerAESKey: testAESKeyHex2,
		OtherServerAESIV:  testAESIVHex2,
	})
	payload := map[string]any{"server": "selection"}

	encryptedEN, err := serverCryptor.Pack(payload, harukiUtils.SupportedDataUploadServerEN)
	if err != nil {
		t.Fatalf("Pack EN failed: %v", err)
	}
	enReference, err := NewSekaiCryptorFromHex(testAESKeyHex, testAESIVHex)
	if err != nil {
		t.Fatalf("create EN reference cryptor: %v", err)
	}
	wantEN, err := enReference.Pack(payload)
	if err != nil {
		t.Fatalf("pack EN reference: %v", err)
	}
	if !bytes.Equal(encryptedEN, wantEN) {
		t.Fatal("EN payload did not use the configured EN key and IV")
	}

	otherReference, err := NewSekaiCryptorFromHex(testAESKeyHex2, testAESIVHex2)
	if err != nil {
		t.Fatalf("create other-server reference cryptor: %v", err)
	}
	for _, server := range []harukiUtils.SupportedDataUploadServer{
		harukiUtils.SupportedDataUploadServerJP,
		harukiUtils.SupportedDataUploadServerTW,
		harukiUtils.SupportedDataUploadServerKR,
		harukiUtils.SupportedDataUploadServerCN,
	} {
		encrypted, err := serverCryptor.Pack(payload, server)
		if err != nil {
			t.Fatalf("Pack %s failed: %v", server, err)
		}
		want, err := otherReference.Pack(payload)
		if err != nil {
			t.Fatalf("pack other-server reference: %v", err)
		}
		if !bytes.Equal(encrypted, want) {
			t.Fatalf("%s payload did not use the configured other-server key and IV", server)
		}
	}
}

func TestServerCryptorInstancesAreIsolated(t *testing.T) {
	first := testServerCryptor()
	second := NewServerCryptor(ServerCryptorConfig{
		ENServerAESKey:    testAESKeyHex2,
		ENServerAESIV:     testAESIVHex2,
		OtherServerAESKey: testAESKeyHex2,
		OtherServerAESIV:  testAESIVHex2,
	})
	payload := map[string]any{"instance": "first"}

	firstCiphertext, err := first.Pack(payload, harukiUtils.SupportedDataUploadServerEN)
	if err != nil {
		t.Fatalf("first Pack failed: %v", err)
	}
	secondCiphertext, err := second.Pack(payload, harukiUtils.SupportedDataUploadServerEN)
	if err != nil {
		t.Fatalf("second Pack failed: %v", err)
	}
	if bytes.Equal(firstCiphertext, secondCiphertext) {
		t.Fatal("independent ServerCryptor instances produced identical ciphertext with different material")
	}
	if _, err := first.Unpack(firstCiphertext, harukiUtils.SupportedDataUploadServerEN); err != nil {
		t.Fatalf("first instance could not unpack its payload: %v", err)
	}
	if _, err := second.Unpack(secondCiphertext, harukiUtils.SupportedDataUploadServerEN); err != nil {
		t.Fatalf("second instance could not unpack its payload: %v", err)
	}
}

func TestServerCryptorZeroValueFailsClosed(t *testing.T) {
	var serverCryptor ServerCryptor
	_, err := serverCryptor.Pack(map[string]any{"a": 1}, harukiUtils.SupportedDataUploadServerJP)
	if err == nil {
		t.Fatal("zero-value ServerCryptor Pack should fail")
	}
	var cryptoErr *CryptoError
	if !errors.As(err, &cryptoErr) || cryptoErr.Operation != "getCryptor" {
		t.Fatalf("zero-value ServerCryptor error = %v, want getCryptor CryptoError", err)
	}
	if !strings.Contains(err.Error(), "invalid iv length: got 0, want 16") {
		t.Fatalf("zero-value ServerCryptor error = %v, want legacy empty key/IV validation error", err)
	}
	if _, err := serverCryptor.Unpack([]byte("invalid"), harukiUtils.SupportedDataUploadServerEN); err == nil {
		t.Fatal("zero-value ServerCryptor Unpack should fail")
	}
}
