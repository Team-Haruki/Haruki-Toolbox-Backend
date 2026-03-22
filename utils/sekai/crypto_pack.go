package sekai

import (
	"crypto/aes"
	"fmt"

	"haruki-suite/utils/orderedmsgpack"

	"github.com/vgorin/cryptogo/pad"
)

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
