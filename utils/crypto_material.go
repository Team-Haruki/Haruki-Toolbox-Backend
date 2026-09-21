package utils

// CryptoMaterial contains hexadecimal Project Sekai client AES key material.
type CryptoMaterial struct {
	Key string `yaml:"key"`
	IV  string `yaml:"iv"`
}
