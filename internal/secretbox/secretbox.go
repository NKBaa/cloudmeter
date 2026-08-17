package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const prefix = "cmsec:v1:"

type Box struct {
	aead cipher.AEAD
}

func New(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("secret encryption key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, prefix)
}

func (b *Box) Encrypt(context, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), []byte(context))
	return prefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (b *Box) Decrypt(context, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if !IsEncrypted(value) {
		return value, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil || len(payload) < b.aead.NonceSize() {
		return "", fmt.Errorf("invalid encrypted secret payload")
	}
	nonce, ciphertext := payload[:b.aead.NonceSize()], payload[b.aead.NonceSize():]
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, []byte(context))
	if err != nil {
		return "", fmt.Errorf("secret authentication failed: %w", err)
	}
	return string(plaintext), nil
}
