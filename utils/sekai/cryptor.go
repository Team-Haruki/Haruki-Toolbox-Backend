package sekai

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"haruki-suite/config"
	"haruki-suite/utils"

	"github.com/vgorin/cryptogo/pad"
	"github.com/vmihailenco/msgpack/v5"
)

func getCipher(server utils.SupportedDataUploadServer, encrypt bool) (cipher.BlockMode, error) {
	var key, iv []byte
	var keyErr, ivErr error
	if server == utils.SupportedDataUploadServerEN {
		key, keyErr = hex.DecodeString(config.Cfg.SekaiClient.ENServerAESKey)
		iv, ivErr = hex.DecodeString(config.Cfg.SekaiClient.ENServerAESIV)
	} else {
		key, keyErr = hex.DecodeString(config.Cfg.SekaiClient.OtherServerAESKey)
		iv, ivErr = hex.DecodeString(config.Cfg.SekaiClient.OtherServerAESIV)
	}
	if keyErr != nil {
		return nil, NewCryptoError("getCipher", fmt.Sprintf("failed to decode AES key for server %s", server), keyErr)
	}
	if ivErr != nil {
		return nil, NewCryptoError("getCipher", fmt.Sprintf("failed to decode AES IV for server %s", server), ivErr)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, NewCryptoError("getCipher", fmt.Sprintf("failed to create AES cipher for server %s", server), err)
	}
	if encrypt {
		return cipher.NewCBCEncrypter(block, iv), nil
	}
	return cipher.NewCBCDecrypter(block, iv), nil
}

func Pack(content interface{}, server utils.SupportedDataUploadServer) ([]byte, error) {
	if content == nil {
		return nil, NewCryptoError("pack", "content cannot be nil", ErrNilContent)
	}
	packed, err := msgpack.Marshal(content)
	if err != nil {
		return nil, NewCryptoError("pack", "failed to serialize content with msgpack", err)
	}
	if len(packed) == 0 {
		return nil, NewCryptoError("pack", "serialized content is empty", ErrEmptyContent)
	}
	padded := pad.PKCS7Pad(packed, aes.BlockSize)
	encrypter, err := getCipher(server, true)
	if err != nil {
		return nil, err // Already wrapped with CryptoError
	}
	encrypted := make([]byte, len(padded))
	encrypter.CryptBlocks(encrypted, padded)
	return encrypted, nil
}

func Unpack(content []byte, server utils.SupportedDataUploadServer) (interface{}, error) {
	if len(content) == 0 {
		return nil, NewCryptoError("unpack", "content cannot be empty", ErrEmptyContent)
	}
	if len(content)%aes.BlockSize != 0 {
		return nil, NewCryptoError("unpack",
			fmt.Sprintf("content length %d is not a multiple of AES block size %d", len(content), aes.BlockSize),
			nil)
	}
	decrypter, err := getCipher(server, false)
	if err != nil {
		return nil, err // Already wrapped with CryptoError
	}
	decrypted := make([]byte, len(content))
	decrypter.CryptBlocks(decrypted, content)
	unpadded, err := pad.PKCS7Unpad(decrypted)
	if err != nil {
		return nil, NewCryptoError("unpack", "failed to remove PKCS7 padding (possibly corrupted data)", err)
	}
	var out interface{}
	if err := msgpack.Unmarshal(unpadded, &out); err != nil {
		return nil, NewCryptoError("unpack", "failed to deserialize content with msgpack", err)
	}
	return out, nil
}
