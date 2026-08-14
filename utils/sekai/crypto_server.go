package sekai

import (
	"fmt"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"github.com/iancoleman/orderedmap"
)

// ServerCryptorConfig contains the Project Sekai client AES material used for
// server payloads. NewServerCryptor copies these strings into an immutable
// value, so independently assembled application instances cannot observe each
// other's configuration.
type ServerCryptorConfig struct {
	ENServerAESKey    string
	ENServerAESIV     string
	OtherServerAESKey string
	OtherServerAESIV  string
}

// ServerCryptor selects the EN client key for EN payloads and the shared
// non-EN client key for every other supported region, matching the historical
// wire behavior. Key validation remains lazy so malformed configuration still
// surfaces at the first pack/unpack operation rather than during assembly.
type ServerCryptor struct {
	enServerAESKey    string
	enServerAESIV     string
	otherServerAESKey string
	otherServerAESIV  string
}

func NewServerCryptor(cfg ServerCryptorConfig) ServerCryptor {
	return ServerCryptor{
		enServerAESKey:    cfg.ENServerAESKey,
		enServerAESIV:     cfg.ENServerAESIV,
		otherServerAESKey: cfg.OtherServerAESKey,
		otherServerAESIV:  cfg.OtherServerAESIV,
	}
}

func (c ServerCryptor) getCryptor(server utils.SupportedDataUploadServer) (*SekaiCryptor, error) {
	var keyHex, ivHex string
	if server == utils.SupportedDataUploadServerEN {
		keyHex = c.enServerAESKey
		ivHex = c.enServerAESIV
	} else {
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
