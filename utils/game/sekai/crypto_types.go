package sekai

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
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
	// Project Sekai's wire protocol mandates AES-CBC with a configured IV and
	// PKCS#7 padding. These payloads use the public game-client key and are
	// attacker-controlled input, not a confidentiality or authenticity boundary.
	if encrypt {
		return cipher.NewCBCEncrypter(c.block, iv) // NOSONAR
	}
	return cipher.NewCBCDecrypter(c.block, iv) // NOSONAR
}

type MsgpackMarshaler interface {
	MarshalMsgpack() ([]byte, error)
}

var bytesPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024)
		return &b
	},
}
