package sekaiclientdummy

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"haruki-suite/utils"

	"github.com/vgorin/cryptogo/pad"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	generalAESKey, _ = hex.DecodeString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")
	generalAESIV, _  = hex.DecodeString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")

	enAESKey, _ = hex.DecodeString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")
	enAESIV, _  = hex.DecodeString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF")
)

func getCipher(server utils.SupportedDataUploadServer, encrypt bool) (cipher.BlockMode, error) {
	var key, iv []byte
	if server == utils.SupportedDataUploadServerEN {
		key, iv = enAESKey, enAESIV
	} else {
		key, iv = generalAESKey, generalAESIV
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if encrypt {
		return cipher.NewCBCEncrypter(block, iv), nil
	}
	return cipher.NewCBCDecrypter(block, iv), nil
}

func Pack(content interface{}, server utils.SupportedDataUploadServer) ([]byte, error) {
	packed, err := msgpack.Marshal(content)
	if err != nil {
		return nil, err
	}

	padded := pad.PKCS7Pad(packed, aes.BlockSize)

	encrypter, err := getCipher(server, true)
	if err != nil {
		return nil, err
	}

	encrypted := make([]byte, len(padded))
	encrypter.CryptBlocks(encrypted, padded)
	return encrypted, nil
}

func Unpack(content []byte, server utils.SupportedDataUploadServer, out interface{}) error {
	decrypter, err := getCipher(server, false)
	if err != nil {
		return err
	}

	decrypted := make([]byte, len(content))
	decrypter.CryptBlocks(decrypted, content)

	unpadded, err := pad.PKCS7Unpad(decrypted)
	if err != nil {
		return err
	}

	return msgpack.Unmarshal(unpadded, out)
}
