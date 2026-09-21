package sekai

import (
	"crypto/aes"
	"fmt"
)

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

func (c *SekaiCryptor) decryptToPooledMsgpack(content []byte) ([]byte, *[]byte, error) {
	if len(content) == 0 {
		return nil, nil, ErrEmptyContent
	}
	if len(content)%aes.BlockSize != 0 {
		return nil, nil, ErrInvalidBlockSize
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
		releasePooledBytes(decrypted)
		return nil, nil, fmt.Errorf("failed to unpad: %w", err)
	}
	return unpadded, decrypted, nil
}

func releasePooledBytes(b *[]byte) {
	if b != nil {
		bytesPool.Put(b)
	}
}
