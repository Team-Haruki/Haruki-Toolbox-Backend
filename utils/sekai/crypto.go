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
	if encrypt {
		return cipher.NewCBCEncrypter(c.block, c.iv)
	}
	return cipher.NewCBCDecrypter(c.block, c.iv)
}

var bytesPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024)
		return &b
	},
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
	defer bytesPool.Put(decrypted)

	decrypter.CryptBlocks(*decrypted, content)

	unpadded, err := pad.PKCS7Unpad(*decrypted)
	if err != nil {
		return fmt.Errorf("failed to unpad: %w", err)
	}

	switch dst := out.(type) {
	case *orderedmap.OrderedMap:
		om, err := orderedmsgpack.MsgpackToOrderedMap(unpadded)
		if err != nil {
			return fmt.Errorf("ordered decode: %w", err)
		}
		om.SetEscapeHTML(false)
		*dst = *om
		return nil
	case **orderedmap.OrderedMap:
		om, err := orderedmsgpack.MsgpackToOrderedMap(unpadded)
		if err != nil {
			return fmt.Errorf("ordered (**ptr) decode: %w", err)
		}
		*dst = om
		return nil
	default:
		if err := orderedmsgpack.Unmarshal(unpadded, out); err != nil {
			return fmt.Errorf("msgpack decode: %w", err)
		}
		return nil
	}
}

func (c *SekaiCryptor) Unpack(content []byte) (any, error) {
	var result any
	if err := c.UnpackInto(content, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *SekaiCryptor) UnpackOrdered(content []byte) (*orderedmap.OrderedMap, error) {
	result := orderedmap.New()
	result.SetEscapeHTML(false)
	if err := c.UnpackInto(content, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *SekaiCryptor) Pack(content any) ([]byte, error) {
	if content == nil {
		return nil, ErrNilContent
	}
	packed, err := orderedmsgpack.Marshal(content)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize content with msgpack: %w", err)
	}
	if len(packed) == 0 {
		return nil, ErrEmptyContent
	}

	padded := pad.PKCS7Pad(packed, aes.BlockSize)
	encrypter := c.newCBC(true)

	encrypted := make([]byte, len(padded))
	encrypter.CryptBlocks(encrypted, padded)
	return encrypted, nil
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

func Pack(content interface{}, server utils.SupportedDataUploadServer) ([]byte, error) {
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

func Unpack(content []byte, server utils.SupportedDataUploadServer) (interface{}, error) {
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
