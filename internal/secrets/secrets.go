// Package secrets provides authenticated symmetric encryption for secrets that
// must be stored at rest (e.g. AI provider API keys). It uses AES-256-GCM; the
// caller-supplied key material is SHA-256'd to a 32-byte key, so any non-empty
// string works as the configured key without base64/length ceremony.
//
// Ciphertext format: base64( nonce || gcm-sealed(plaintext) ). Each Encrypt
// uses a fresh random nonce, so encrypting the same plaintext twice yields
// different ciphertexts.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

// Encryptor seals/opens secrets with a fixed key. Safe for concurrent use.
type Encryptor struct {
	gcm cipher.AEAD
}

// NewEncryptor derives a 32-byte AES-256 key from `key` via SHA-256. It never
// fails for a non-empty key (AES/GCM construction with a 32-byte key can't
// error), but returns an error on empty input to catch misconfiguration.
func NewEncryptor(key string) (*Encryptor, error) {
	if key == "" {
		return nil, errors.New("secrets: encryption key must not be empty")
	}
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encryptor{gcm: gcm}, nil
}

// Encrypt returns base64(nonce || ciphertext) for the given plaintext.
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := e.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt. Returns an error if the input is malformed or the
// authentication tag doesn't verify (wrong key or tampering).
func (e *Encryptor) Decrypt(encoded string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	ns := e.gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("secrets: ciphertext too short")
	}
	nonce, ciphertext := data[:ns], data[ns:]
	plaintext, err := e.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}
