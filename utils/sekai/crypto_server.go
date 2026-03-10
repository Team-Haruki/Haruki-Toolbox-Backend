package sekai

import (
	"fmt"

	"haruki-suite/config"
	"haruki-suite/utils"

	"github.com/iancoleman/orderedmap"
)

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

	unpadded, pooled, err := cryptor.decryptToPooledMsgpack(content)
	if err != nil {
		return nil, err
	}
	defer releasePooledBytes(pooled)

	result := make([]byte, len(unpadded))
	copy(result, unpadded)
	return result, nil
}
