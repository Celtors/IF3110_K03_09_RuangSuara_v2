package databasemanagement

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func encryptionKey() ([]byte, error) {
	v := strings.TrimSpace(os.Getenv("DB_CREDENTIALS_KEY"))
	if v == "" {
		return nil, errors.New("DB_CREDENTIALS_KEY is not configured")
	}
	key, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(key) != 32 {
		return nil, errors.New("DB_CREDENTIALS_KEY must be base64-encoded 32-byte key")
	}
	return key, nil
}
func encryptPassword(plain string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
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
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
func decryptPassword(encoded string) (string, error) {
	key, err := encryptionKey()
	if err != nil {
		return "", err
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid encrypted credential")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted credential")
	}
	plain, err := gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("unable to decrypt target credential")
	}
	return string(plain), nil
}
