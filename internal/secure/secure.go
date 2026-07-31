package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("encryption key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plaintext string, associatedData []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), associatedData)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (b *Box) Open(encoded string, associatedData []byte) (string, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}
	nonceSize := b.aead.NonceSize()
	if len(sealed) < nonceSize {
		return "", fmt.Errorf("ciphertext is too short")
	}
	plaintext, err := b.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], associatedData)
	if err != nil {
		return "", fmt.Errorf("open ciphertext: %w", err)
	}
	return string(plaintext), nil
}
