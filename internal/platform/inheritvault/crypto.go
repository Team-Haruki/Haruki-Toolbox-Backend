// Package inheritvault keeps game login credentials and transfer codes that a
// Toolbox inherit job obtained, so the owner can get back into the account.
//
// Every record is sealed to the owner's own X25519 public key. The matching
// private key is wrapped twice: first with a key derived from the owner's
// Toolbox password (or vault PIN) with Argon2id, then with the server master
// key. Opening a record therefore needs the database row, the master key and
// the owner's secret together; the server alone cannot decrypt anything once
// the owner's secret has left memory.
package inheritvault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// KeyWrapScheme names how a user's private key is protected.
	KeyWrapScheme = "x25519-argon2id-v1"
	// EntryWrapScheme names how a vault record is sealed to a public key.
	EntryWrapScheme = "x25519-hkdf-sha256-aes256gcm-v1"

	sealVersion        byte = 1
	keySize                 = 32
	nonceSize               = 12
	saltSize                = 16
	sealInfo                = "haruki/inherit-vault/seal/v1"
	liveSealInfo            = "haruki/inherit-job/live-code/v1"
	keySecretADPrefix       = "haruki/inherit-vault/key/v1|secret|"
	keyMasterADPrefix       = "haruki/inherit-vault/key/v1|master|"
	entryADPrefix           = "haruki/inherit-vault/entry/v1|"
	liveADPrefix            = "haruki/inherit-job/live-code/v1|"
	maxSecretBytes          = 1024
	maximumMemoryKiB        = 1024 * 1024
	maximumIterations       = 16
	maximumParallelism      = 8
)

var (
	// ErrDecrypt means a seal did not open: wrong secret, wrong key, a
	// tampered blob, or associated data that does not match.
	ErrDecrypt = errors.New("inherit vault: decryption failed")
	// ErrMasterKey reports an unusable master key file.
	ErrMasterKey = errors.New("inherit vault: invalid master key")
)

// MasterKey is the server-side key that wraps every user's private key. It
// is loaded once from a file and never logged.
type MasterKey struct {
	version int
	aead    cipher.AEAD
	live    cipher.AEAD
}

// Version is the master key version recorded with every wrapped key.
func (m *MasterKey) Version() int { return m.version }

// String keeps the key out of any accidental %v.
func (m *MasterKey) String() string {
	return "inheritvault.MasterKey(v" + strconv.Itoa(m.version) + ")"
}

// LoadMasterKeyFile reads a 32-byte key: either the raw bytes, or 64 hex
// characters, or standard base64 of 32 bytes (surrounding whitespace is
// ignored for the text forms).
func LoadMasterKeyFile(path string, version int) (*MasterKey, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("%w: no key file configured", ErrMasterKey)
	}
	if version <= 0 {
		return nil, fmt.Errorf("%w: version must be positive", ErrMasterKey)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot read key file", ErrMasterKey)
	}
	key, err := decodeKeyMaterial(raw)
	clear(raw)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return NewMasterKey(key, version)
}

func decodeKeyMaterial(raw []byte) ([]byte, error) {
	if len(raw) == keySize {
		return append([]byte(nil), raw...), nil
	}
	text := strings.TrimSpace(string(raw))
	if len(text) == 2*keySize {
		if key, err := hex.DecodeString(text); err == nil {
			return key, nil
		}
	}
	if key, err := base64.StdEncoding.DecodeString(text); err == nil && len(key) == keySize {
		return key, nil
	}
	return nil, fmt.Errorf("%w: key file must hold exactly 32 bytes (raw, hex or base64)", ErrMasterKey)
}

// NewMasterKey builds a master key from 32 bytes; the caller may clear key
// afterwards.
func NewMasterKey(key []byte, version int) (*MasterKey, error) {
	if len(key) != keySize || version <= 0 {
		return nil, ErrMasterKey
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	liveKey, err := hkdf.Key(sha256.New, key, nil, liveSealInfo, keySize)
	if err != nil {
		return nil, err
	}
	defer clear(liveKey)
	live, err := newGCM(liveKey)
	if err != nil {
		return nil, err
	}
	return &MasterKey{version: version, aead: aead, live: live}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealGCM(aead cipher.AEAD, plaintext, ad []byte) []byte {
	nonce := make([]byte, nonceSize, nonceSize+len(plaintext)+aead.Overhead())
	_, _ = rand.Read(nonce)
	return aead.Seal(nonce, nonce, plaintext, ad)
}

func openGCM(aead cipher.AEAD, blob, ad []byte) ([]byte, error) {
	if len(blob) < nonceSize+aead.Overhead() {
		return nil, ErrDecrypt
	}
	out, err := aead.Open(nil, blob[:nonceSize], blob[nonceSize:], ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}

// KDFParams are the Argon2id parameters a private key was wrapped with. They
// are stored with the key so they can be raised later without breaking old
// keys.
type KDFParams struct {
	Algorithm   string `json:"alg"`
	Iterations  uint32 `json:"t"`
	MemoryKiB   uint32 `json:"m"`
	Parallelism uint8  `json:"p"`
	Salt        []byte `json:"salt"`
}

// DefaultKDFParams is used for new keys. Tests lower it.
var DefaultKDFParams = KDFParams{Algorithm: "argon2id", Iterations: 3, MemoryKiB: 64 * 1024, Parallelism: 1}

// kdfSlots bounds concurrent Argon2id derivations (each holds MemoryKiB).
var kdfSlots = make(chan struct{}, 4)

func (p KDFParams) validate() error {
	if p.Algorithm != "argon2id" || len(p.Salt) < saltSize ||
		p.Iterations == 0 || p.Iterations > maximumIterations ||
		p.MemoryKiB < 8*uint32(p.Parallelism) || p.MemoryKiB > maximumMemoryKiB ||
		p.Parallelism == 0 || p.Parallelism > maximumParallelism {
		return errors.New("inherit vault: unsupported KDF parameters")
	}
	return nil
}

func deriveSecretKey(secret []byte, p KDFParams) ([]byte, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if len(secret) == 0 || len(secret) > maxSecretBytes {
		return nil, ErrDecrypt
	}
	kdfSlots <- struct{}{}
	defer func() { <-kdfSlots }()
	return argon2.IDKey(secret, p.Salt, p.Iterations, p.MemoryKiB, p.Parallelism, keySize), nil
}

// WrappedKey is a user's key pair as stored: the public key in the clear and
// the private key wrapped by the user's secret and then by the master key.
type WrappedKey struct {
	UserID           string
	KeyVersion       int
	PublicKey        []byte
	WrappedPrivate   []byte
	KDF              KDFParams
	MasterKeyVersion int
	WrapScheme       string
}

func keySecretAD(userID string, keyVersion int) []byte {
	return []byte(keySecretADPrefix + userID + "|" + strconv.Itoa(keyVersion))
}

func keyMasterAD(userID string, keyVersion, masterVersion int) []byte {
	return []byte(keyMasterADPrefix + userID + "|" + strconv.Itoa(keyVersion) + "|" + strconv.Itoa(masterVersion))
}

// GenerateKey creates a key pair for userID and wraps its private key with
// secret and master.
func GenerateKey(master *MasterKey, userID string, keyVersion int, secret []byte) (*WrappedKey, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	raw := priv.Bytes()
	defer clear(raw)
	params := DefaultKDFParams
	params.Salt = make([]byte, saltSize)
	_, _ = rand.Read(params.Salt)
	return wrapPrivateKey(master, userID, keyVersion, raw, priv.PublicKey().Bytes(), secret, params)
}

func wrapPrivateKey(master *MasterKey, userID string, keyVersion int, raw, public, secret []byte, params KDFParams) (*WrappedKey, error) {
	if master == nil {
		return nil, ErrMasterKey
	}
	kek, err := deriveSecretKey(secret, params)
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	inner, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	sealedBySecret := sealGCM(inner, raw, keySecretAD(userID, keyVersion))
	return &WrappedKey{
		UserID:           userID,
		KeyVersion:       keyVersion,
		PublicKey:        append([]byte(nil), public...),
		WrappedPrivate:   sealGCM(master.aead, sealedBySecret, keyMasterAD(userID, keyVersion, master.version)),
		KDF:              params,
		MasterKeyVersion: master.version,
		WrapScheme:       KeyWrapScheme,
	}, nil
}

// UnwrapPrivateKey returns the raw private key. The caller must clear it as
// soon as it is done. ErrDecrypt means secret is not the one the key was
// wrapped with (or the record was tampered with).
func UnwrapPrivateKey(master *MasterKey, k *WrappedKey, secret []byte) ([]byte, error) {
	if master == nil || k == nil {
		return nil, ErrMasterKey
	}
	if k.WrapScheme != KeyWrapScheme {
		return nil, fmt.Errorf("inherit vault: unsupported key wrap scheme %q", k.WrapScheme)
	}
	if k.MasterKeyVersion != master.version {
		return nil, fmt.Errorf("%w: key wrapped by master key v%d, loaded v%d", ErrMasterKey, k.MasterKeyVersion, master.version)
	}
	sealedBySecret, err := openGCM(master.aead, k.WrappedPrivate, keyMasterAD(k.UserID, k.KeyVersion, k.MasterKeyVersion))
	if err != nil {
		return nil, err
	}
	kek, err := deriveSecretKey(secret, k.KDF)
	if err != nil {
		return nil, err
	}
	defer clear(kek)
	inner, err := newGCM(kek)
	if err != nil {
		return nil, err
	}
	raw, err := openGCM(inner, sealedBySecret, keySecretAD(k.UserID, k.KeyVersion))
	if err != nil {
		return nil, err
	}
	if len(raw) != keySize {
		clear(raw)
		return nil, ErrDecrypt
	}
	return raw, nil
}

// Rewrap re-protects an unwrapped private key with a new secret (after the
// owner changed their Toolbox password, for example), with a fresh salt.
func Rewrap(master *MasterKey, k *WrappedKey, raw, newSecret []byte) (*WrappedKey, error) {
	params := DefaultKDFParams
	params.Salt = make([]byte, saltSize)
	_, _ = rand.Read(params.Salt)
	return wrapPrivateKey(master, k.UserID, k.KeyVersion, raw, k.PublicKey, newSecret, params)
}

// EntryAD binds a sealed value to its vault row, owner, server, key version
// and purpose, so a blob cannot be moved to another row or user.
func EntryAD(purpose, entryID, userID, server string, keyVersion int) []byte {
	return []byte(entryADPrefix + purpose + "|" + entryID + "|" + userID + "|" + server + "|" + strconv.Itoa(keyVersion))
}

// Seal encrypts plaintext to an X25519 public key: an ephemeral key pair,
// HKDF-SHA256 over the shared secret, then AES-256-GCM. The blob is
// version || ephemeral public key || nonce || ciphertext.
func Seal(publicKey, plaintext, ad []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	aead, err := sealAEAD(eph, pub)
	if err != nil {
		return nil, err
	}
	out := append([]byte{sealVersion}, eph.PublicKey().Bytes()...)
	return append(out, sealGCM(aead, plaintext, ad)...), nil
}

// Open decrypts a Seal blob with the raw private key.
func Open(privateKey, blob, ad []byte) ([]byte, error) {
	if len(blob) < 1+keySize || blob[0] != sealVersion {
		return nil, ErrDecrypt
	}
	priv, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, ErrDecrypt
	}
	eph, err := ecdh.X25519().NewPublicKey(blob[1 : 1+keySize])
	if err != nil {
		return nil, ErrDecrypt
	}
	aead, err := openAEAD(priv, eph)
	if err != nil {
		return nil, ErrDecrypt
	}
	return openGCM(aead, blob[1+keySize:], ad)
}

func sealAEAD(eph *ecdh.PrivateKey, recipient *ecdh.PublicKey) (cipher.AEAD, error) {
	shared, err := eph.ECDH(recipient)
	if err != nil {
		return nil, err
	}
	defer clear(shared)
	return hkdfAEAD(shared, eph.PublicKey().Bytes(), recipient.Bytes())
}

func openAEAD(priv *ecdh.PrivateKey, eph *ecdh.PublicKey) (cipher.AEAD, error) {
	shared, err := priv.ECDH(eph)
	if err != nil {
		return nil, err
	}
	defer clear(shared)
	return hkdfAEAD(shared, eph.Bytes(), priv.PublicKey().Bytes())
}

func hkdfAEAD(shared, ephPublic, recipient []byte) (cipher.AEAD, error) {
	salt := append(append([]byte(nil), ephPublic...), recipient...)
	key, err := hkdf.Key(sha256.New, shared, salt, sealInfo, keySize)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	return newGCM(key)
}

// SealLive encrypts a value under the master key alone, bound to a job and
// its owner. It is only for the new transfer code shown during a live job
// (at most an hour, in the job record); the vault itself never uses it.
func (m *MasterKey) SealLive(plaintext []byte, jobID, userID string) []byte {
	return sealGCM(m.live, plaintext, []byte(liveADPrefix+jobID+"|"+userID))
}

// OpenLive reverses SealLive.
func (m *MasterKey) OpenLive(blob []byte, jobID, userID string) ([]byte, error) {
	return openGCM(m.live, blob, []byte(liveADPrefix+jobID+"|"+userID))
}
