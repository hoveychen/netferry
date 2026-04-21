package profile

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// ExportKey is the AES-256 key (hex-encoded, 64 chars) used to decrypt
// exported .nfprofile files. Must match the desktop NETFERRY_EXPORT_KEY.
// Set at build time via ldflags:
//
//	go build -ldflags="-X github.com/hoveychen/netferry/relay/internal/profile.ExportKey=<hex>" ...
var ExportKey = ""

func exportKeyBytes() ([]byte, error) {
	if ExportKey == "" {
		return nil, fmt.Errorf("profile export is not available in this build (NETFERRY_EXPORT_KEY not baked in)")
	}
	key, err := hex.DecodeString(ExportKey)
	if err != nil {
		return nil, fmt.Errorf("invalid export key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("export key must be 32 bytes (64 hex chars)")
	}
	return key, nil
}

// Decrypt takes a base64-encoded ciphertext (nonce || ciphertext+tag) produced
// by the desktop `encrypt` function and returns the plaintext bytes.
func Decrypt(encrypted string) ([]byte, error) {
	key, err := exportKeyBytes()
	if err != nil {
		return nil, err
	}

	combined, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	if len(combined) < 13 {
		return nil, fmt.Errorf("encrypted data too short")
	}

	nonce := combined[:12]
	ciphertext := combined[12:]

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed — wrong key or corrupted data")
	}
	return plaintext, nil
}
