package sekai

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"haruki-suite/config"
	"haruki-suite/utils"
	"haruki-suite/utils/orderedmsgpack"

	"github.com/iancoleman/orderedmap"
	"github.com/shamaton/msgpack/v2"
	"github.com/vgorin/cryptogo/pad"
)

var (
	ErrInvalidBlockSize = errors.New("content length is not a multiple of AES block size")
)

type SekaiCryptor struct {
	key   []byte
	iv    []byte
	block cipher.Block
}

func NewSekaiCryptorFromHex(aesKeyHex, aesIVHex string) (*SekaiCryptor, error) {
	key, err := hex.DecodeString(aesKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid aes key hex: %w", err)
	}
	iv, err := hex.DecodeString(aesIVHex)
	if err != nil {
		return nil, fmt.Errorf("invalid aes iv hex: %w", err)
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("invalid iv length: got %d, want %d", len(iv), aes.BlockSize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	return &SekaiCryptor{
		key:   key,
		iv:    iv,
		block: block,
	}, nil
}

func (c *SekaiCryptor) newCBC(encrypt bool) cipher.BlockMode {
	iv := make([]byte, len(c.iv))
	copy(iv, c.iv)
	if encrypt {
		return cipher.NewCBCEncrypter(c.block, iv)
	}
	return cipher.NewCBCDecrypter(c.block, iv)
}

type MsgpackMarshaler interface {
	MarshalMsgpack() ([]byte, error)
}

func (c *SekaiCryptor) Pack(content any) ([]byte, error) {
	if content == nil {
		return nil, ErrNilContent
	}

	var raw []byte
	switch v := content.(type) {
	case []byte:
		raw = v
	default:
		if marshaler, ok := content.(MsgpackMarshaler); ok {
			b, err := marshaler.MarshalMsgpack()
			if err != nil {
				return nil, fmt.Errorf("custom msgpack marshal: %w", err)
			}
			raw = b
		} else {
			b, err := orderedmsgpack.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("msgpack marshal: %w", err)
			}
			raw = b
		}
	}

	if len(raw) == 0 {
		return nil, ErrEmptyContent
	}

	padded := pad.PKCS7Pad(raw, aes.BlockSize)
	encrypter := c.newCBC(true)
	encrypted := make([]byte, len(padded))
	encrypter.CryptBlocks(encrypted, padded)
	return encrypted, nil
}

var bytesPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024)
		return &b
	},
}

func safePKCS7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, ErrInvalidBlockSize
	}

	padLen := int(b[len(b)-1])
	if padLen <= 0 || padLen > aes.BlockSize {
		return nil, fmt.Errorf("invalid pkcs7 padding length: %d", padLen)
	}

	if padLen > len(b) {
		return nil, fmt.Errorf("invalid pkcs7 padding length exceeds data length")
	}

	for i := len(b) - padLen; i < len(b); i++ {
		if int(b[i]) != padLen {
			return nil, fmt.Errorf("invalid pkcs7 padding byte at %d: %d", i, b[i])
		}
	}

	return b[:len(b)-padLen], nil
}

func (c *SekaiCryptor) UnpackInto(content []byte, out any) error {
	if len(content) == 0 {
		return ErrEmptyContent
	}
	if len(content)%aes.BlockSize != 0 {
		return ErrInvalidBlockSize
	}
	if out == nil {
		return fmt.Errorf("out must be a non-nil pointer")
	}

	decrypter := c.newCBC(false)

	decrypted := bytesPool.Get().(*[]byte)
	if cap(*decrypted) < len(content) {
		*decrypted = make([]byte, len(content))
	} else {
		*decrypted = (*decrypted)[:len(content)]
	}

	decrypter.CryptBlocks(*decrypted, content)

	unpadded, err := safePKCS7Unpad(*decrypted)
	if err != nil {
		bytesPool.Put(decrypted)
		return fmt.Errorf("failed to unpad: %w", err)
	}

	switch dst := out.(type) {
	case *orderedmap.OrderedMap:
		om, err := orderedmsgpack.MsgpackToOrderedMap(unpadded)
		if err != nil {
			bytesPool.Put(decrypted)
			return fmt.Errorf("ordered decode: %w", err)
		}
		om.SetEscapeHTML(false)
		*dst = *om
	case **orderedmap.OrderedMap:
		om, err := orderedmsgpack.MsgpackToOrderedMap(unpadded)
		if err != nil {
			bytesPool.Put(decrypted)
			return fmt.Errorf("ordered (**ptr) decode: %w", err)
		}
		*dst = om
	default:
		if err := msgpack.Unmarshal(unpadded, out); err != nil {
			bytesPool.Put(decrypted)
			preview := unpadded
			if len(preview) > 200 {
				preview = preview[:200]
			}
			return fmt.Errorf("msgpack decode (len=%d, target=%T, first200=%x): %w", len(unpadded), out, preview, err)
		}
	}

	bytesPool.Put(decrypted)
	return nil
}

func (c *SekaiCryptor) Unpack(content []byte) (any, error) {
	var mapResult map[string]any
	if err := c.UnpackInto(content, &mapResult); err == nil {
		sanitizeMapValues(mapResult)
		return mapResult, nil
	}

	var sliceResult []any
	if err := c.UnpackInto(content, &sliceResult); err == nil {
		sanitizeSliceValues(sliceResult)
		return sliceResult, nil
	}

	var anyResult any
	if err := c.UnpackInto(content, &anyResult); err != nil {
		return nil, err
	}
	return convertUnpackResult(anyResult), nil
}

func sanitizeMapValues(m map[string]any) {
	for k, v := range m {
		switch child := v.(type) {
		case map[any]any:
			m[k] = convertUnpackResult(child)
		case map[string]any:
			sanitizeMapValues(child)
		case []any:
			sanitizeSliceValues(child)
		}
	}
}

func sanitizeSliceValues(s []any) {
	for i, v := range s {
		switch child := v.(type) {
		case map[any]any:
			s[i] = convertUnpackResult(child)
		case map[string]any:
			sanitizeMapValues(child)
		case []any:
			sanitizeSliceValues(child)
		}
	}
}

func convertUnpackResult(v any) any {
	switch x := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(x))
		for k, val := range x {
			if keyStr, ok := k.(string); ok {
				m[keyStr] = convertUnpackResult(val)
			} else if keyBytes, ok := k.([]byte); ok {
				m[string(keyBytes)] = convertUnpackResult(val)
			}
		}
		return m
	case map[string]any:
		sanitizeMapValues(x)
		return x
	case []any:
		sanitizeSliceValues(x)
		return x
	default:
		return v
	}
}

func (c *SekaiCryptor) UnpackOrdered(content []byte) (*orderedmap.OrderedMap, error) {
	result := orderedmap.New()
	result.SetEscapeHTML(false)
	if err := c.UnpackInto(content, result); err != nil {
		return nil, err
	}
	return result, nil
}

func getCryptor(server utils.SupportedDataUploadServer) (*SekaiCryptor, error) {
	var keyHex, ivHex string
	if server == utils.SupportedDataUploadServerEN {
		keyHex = config.Cfg.SekaiClient.ENServerAESKey
		ivHex = config.Cfg.SekaiClient.ENServerAESIV
	} else {
		keyHex = config.Cfg.SekaiClient.OtherServerAESKey
		ivHex = config.Cfg.SekaiClient.OtherServerAESIV
	}

	cryptor, err := NewSekaiCryptorFromHex(keyHex, ivHex)
	if err != nil {
		return nil, NewCryptoError("getCryptor", fmt.Sprintf("failed to create cryptor for server %s", server), err)
	}
	return cryptor, nil
}

func Pack(content any, server utils.SupportedDataUploadServer) ([]byte, error) {
	cryptor, err := getCryptor(server)
	if err != nil {
		return nil, err
	}
	result, err := cryptor.Pack(content)
	if err != nil {
		return nil, NewCryptoError("pack", "failed to pack content", err)
	}
	return result, nil
}

func Unpack(content []byte, server utils.SupportedDataUploadServer) (any, error) {
	cryptor, err := getCryptor(server)
	if err != nil {
		return nil, err
	}
	result, err := cryptor.Unpack(content)
	if err != nil {
		return nil, NewCryptoError("unpack", "failed to unpack content", err)
	}
	return result, nil
}

func UnpackOrdered(content []byte, server utils.SupportedDataUploadServer) (*orderedmap.OrderedMap, error) {
	cryptor, err := getCryptor(server)
	if err != nil {
		return nil, err
	}
	result, err := cryptor.UnpackOrdered(content)
	if err != nil {
		return nil, NewCryptoError("unpackOrdered", "failed to unpack ordered content", err)
	}
	return result, nil
}

func DecryptToMsgpack(content []byte, server utils.SupportedDataUploadServer) ([]byte, error) {
	cryptor, err := getCryptor(server)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return nil, ErrEmptyContent
	}
	if len(content)%aes.BlockSize != 0 {
		return nil, ErrInvalidBlockSize
	}

	decrypter := cryptor.newCBC(false)

	decrypted := bytesPool.Get().(*[]byte)
	if cap(*decrypted) < len(content) {
		*decrypted = make([]byte, len(content))
	} else {
		*decrypted = (*decrypted)[:len(content)]
	}

	decrypter.CryptBlocks(*decrypted, content)

	unpadded, err := safePKCS7Unpad(*decrypted)
	if err != nil {
		bytesPool.Put(decrypted)
		return nil, fmt.Errorf("failed to unpad: %w", err)
	}

	result := make([]byte, len(unpadded))
	copy(result, unpadded)
	bytesPool.Put(decrypted)

	return result, nil
}
