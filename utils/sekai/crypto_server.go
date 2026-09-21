package sekai

import (
	"fmt"
	"maps"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils"

	"github.com/Team-Haruki/Haruki-Toolbox-Backend/utils/orderedmap"
)

// ServerCryptorConfig contains the Project Sekai client AES material used for
// server payloads. NewServerCryptor copies these strings into an immutable
// value, so independently assembled application instances cannot observe each
// other's configuration.
type ServerCryptorConfig struct {
	Regions map[string]utils.CryptoMaterial
}

// ServerCryptor owns an immutable copy of each region's client AES material.
// Validation of hexadecimal key material remains lazy at pack/unpack time.
type ServerCryptor struct {
	regions map[string]utils.CryptoMaterial
}

func NewServerCryptor(cfg ServerCryptorConfig) ServerCryptor {
	return ServerCryptor{regions: maps.Clone(cfg.Regions)}
}
func (c ServerCryptor) getCryptor(server utils.SupportedDataUploadServer) (*SekaiCryptor, error) {
	pair := c.regions[string(server)]
	cryptor, err := NewSekaiCryptorFromHex(pair.Key, pair.IV)
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
