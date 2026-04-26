package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// EncryptStringAESGCM encrypts plaintext using AES-256-GCM with the first key in keyring.
// Ciphertext format: v1:<key_index>:<nonce_b64>:<ct_b64>
func EncryptStringAESGCM(keyring []string, plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if len(keyring) == 0 {
		return "", errors.New("missing oauth2 token keyring")
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(keyring[0]))
	if err != nil {
		return "", fmt.Errorf("invalid oauth2 token key: %v", err)
	}
	if len(key) != 32 {
		return "", errors.New("oauth2 token key must be 32 bytes base64")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return fmt.Sprintf("v1:%d:%s:%s", 0,
		base64.RawStdEncoding.EncodeToString(nonce),
		base64.RawStdEncoding.EncodeToString(ct),
	), nil
}

// DecryptStringAESGCM decrypts ciphertext produced by EncryptStringAESGCM.
// Tries all keys in keyring to support rotation.
func DecryptStringAESGCM(keyring []string, ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	parts := strings.Split(ciphertext, ":")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", errors.New("invalid ciphertext format")
	}
	nonceB64 := parts[2]
	ctB64 := parts[3]
	nonce, err := base64.RawStdEncoding.DecodeString(nonceB64)
	if err != nil {
		return "", errors.New("invalid nonce")
	}
	ct, err := base64.RawStdEncoding.DecodeString(ctB64)
	if err != nil {
		return "", errors.New("invalid ciphertext")
	}
	var lastErr error
	for i := range keyring {
		kb64 := strings.TrimSpace(keyring[i])
		if kb64 == "" {
			continue
		}
		key, err := base64.RawStdEncoding.DecodeString(kb64)
		if err != nil || len(key) != 32 {
			lastErr = errors.New("invalid oauth2 token key")
			continue
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			lastErr = err
			continue
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			lastErr = err
			continue
		}
		if len(nonce) != gcm.NonceSize() {
			lastErr = errors.New("invalid nonce size")
			continue
		}
		pt, err := gcm.Open(nil, nonce, ct, nil)
		if err == nil {
			return string(pt), nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no valid keys")
	}
	return "", lastErr
}

