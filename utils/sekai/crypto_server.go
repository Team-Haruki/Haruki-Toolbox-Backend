package sekai

import (
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
)

// ServerCryptorConfig contains the Project Sekai client AES material used for
// server payloads. NewServerCryptor copies these strings into an immutable
// value, so independently assembled application instances cannot observe each
// other's configuration.
type ServerCryptorConfig struct {
	ENServerAESKey    string
	ENServerAESIV     string
	CNServerAESKey    string
	CNServerAESIV     string
	OtherServerAESKey string
	OtherServerAESIV  string
}

// ServerCryptor selects dedicated EN and CN client keys and the shared key for
// every other supported region. An empty CN pair falls back to the shared key
// for backward compatibility. Key validation remains lazy so malformed
// configuration surfaces at the first pack/unpack operation.
type ServerCryptor struct {
	enServerAESKey    string
	enServerAESIV     string
	cnServerAESKey    string
	cnServerAESIV     string
	otherServerAESKey string
	otherServerAESIV  string
}

func NewServerCryptor(cfg ServerCryptorConfig) ServerCryptor {
	return ServerCryptor{
		enServerAESKey:    cfg.ENServerAESKey,
		enServerAESIV:     cfg.ENServerAESIV,
		cnServerAESKey:    cfg.CNServerAESKey,
		cnServerAESIV:     cfg.CNServerAESIV,
		otherServerAESKey: cfg.OtherServerAESKey,
		otherServerAESIV:  cfg.OtherServerAESIV,
	}
}

func (c ServerCryptor) getCryptor(server utils.SupportedDataUploadServer) (*SekaiCryptor, error) {
	var keyHex, ivHex string
	switch server {
	case utils.SupportedDataUploadServerEN:
		keyHex = c.enServerAESKey
		ivHex = c.enServerAESIV
	case utils.SupportedDataUploadServerCN:
		keyHex = c.cnServerAESKey
		ivHex = c.cnServerAESIV
		if keyHex == "" && ivHex == "" {
			keyHex = c.otherServerAESKey
			ivHex = c.otherServerAESIV
		}
	default:
		keyHex = c.otherServerAESKey
		ivHex = c.otherServerAESIV
	}

	cryptor, err := NewSekaiCryptorFromHex(keyHex, ivHex)
	if err != nil {
		return nil, NewCryptoError("getCryptor", fmt.Sprintf("failed to create cryptor for server %s", server), err)
	}
	return cryptor, nil
}

func (c ServerCryptor) Pack(content any, server utils.SupportedDataUploadServer) ([]byte, error) {
	cryptor, err := c.getCryptor(server)
	if err != nil {
		return nil, err
	}
	result, err := cryptor.Pack(content)
	if err != nil {
		return nil, NewCryptoError("pack", "failed to pack content", err)
	}
	return result, nil
}

func (c ServerCryptor) Unpack(content []byte, server utils.SupportedDataUploadServer) (any, error) {
	cryptor, err := c.getCryptor(server)
	if err != nil {
		return nil, err
	}
	result, err := cryptor.Unpack(content)
	if err != nil {
		return nil, NewCryptoError("unpack", "failed to unpack content", err)
	}
	return result, nil
}

func (c ServerCryptor) UnpackOrdered(content []byte, server utils.SupportedDataUploadServer) (*orderedmap.OrderedMap, error) {
	cryptor, err := c.getCryptor(server)
	if err != nil {
		return nil, err
	}
	result, err := cryptor.UnpackOrdered(content)
	if err != nil {
		return nil, NewCryptoError("unpackOrdered", "failed to unpack ordered content", err)
	}
	return result, nil
}

func (c ServerCryptor) DecryptToMsgpack(content []byte, server utils.SupportedDataUploadServer) ([]byte, error) {
	cryptor, err := c.getCryptor(server)
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
