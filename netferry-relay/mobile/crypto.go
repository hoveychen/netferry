package mobile

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ExportKey is the AES-256 key (hex-encoded, 64 chars) used to encrypt/decrypt
// exported profiles. Must match the desktop NETFERRY_EXPORT_KEY.
// Set at build time via ldflags:
//
//	gomobile bind -ldflags="-X ...mobile.ExportKey=<hex>" ...
var ExportKey = ""

func exportKeyBytes() ([]byte, error) {
	if ExportKey == "" {
		return nil, fmt.Errorf("profile export is not available in this build")
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

// DecryptProfile decrypts a base64-encoded, AES-256-GCM encrypted profile
// string (as produced by desktop export or QR code chunks) and returns the
// profile JSON.
func DecryptProfile(encrypted string) (string, error) {
	key, err := exportKeyBytes()
	if err != nil {
		return "", err
	}

	combined, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil {
		return "", fmt.Errorf("invalid base64: %w", err)
	}
	if len(combined) < 13 {
		return "", fmt.Errorf("encrypted data too short")
	}

	nonce := combined[:12]
	ciphertext := combined[12:]

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed — wrong key or corrupted data")
	}

	return string(plaintext), nil
}

// EncryptProfile encrypts a profile JSON string with AES-256-GCM and returns
// a base64-encoded string compatible with desktop import.
func EncryptProfile(profileJSON string) (string, error) {
	key, err := exportKeyBytes()
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

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(profileJSON), nil)
	combined := append(nonce, ciphertext...)
	return base64.StdEncoding.EncodeToString(combined), nil
}

// QRChunkInfo holds parsed QR chunk metadata. gomobile cannot export
// functions with more than 2 return values, so we use a struct.
type QRChunkInfo struct {
	Index int32
	Total int32
	Data  string
}

// ParseQRChunk parses a QR code chunk in the format "NF:{index}/{total}:{data}".
// Index is 1-based.
func ParseQRChunk(chunk string) (*QRChunkInfo, error) {
	if !strings.HasPrefix(chunk, "NF:") {
		return nil, fmt.Errorf("not a NetFerry QR code")
	}
	rest := chunk[3:]
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return nil, fmt.Errorf("invalid QR chunk format")
	}
	colonIdx := strings.Index(rest[slashIdx:], ":")
	if colonIdx < 0 {
		return nil, fmt.Errorf("invalid QR chunk format")
	}
	colonIdx += slashIdx

	idx, err := strconv.Atoi(rest[:slashIdx])
	if err != nil {
		return nil, fmt.Errorf("invalid chunk index")
	}
	tot, err := strconv.Atoi(rest[slashIdx+1 : colonIdx])
	if err != nil {
		return nil, fmt.Errorf("invalid chunk total")
	}

	return &QRChunkInfo{
		Index: int32(idx),
		Total: int32(tot),
		Data:  rest[colonIdx+1:],
	}, nil
}

// ImportFromQR reassembles QR chunks and decrypts the profile.
// chunks is a JSON array of QR code strings: ["NF:1/3:...", "NF:2/3:...", "NF:3/3:..."]
// Returns the profile JSON string.
func ImportFromQR(chunksJSON string) (string, error) {
	var chunks []string
	if err := json.Unmarshal([]byte(chunksJSON), &chunks); err != nil {
		return "", fmt.Errorf("invalid chunks JSON: %w", err)
	}
	if len(chunks) == 0 {
		return "", fmt.Errorf("no QR chunks provided")
	}

	// Parse and sort chunks.
	var total int32
	parts := make(map[int32]string)
	for _, c := range chunks {
		info, err := ParseQRChunk(c)
		if err != nil {
			return "", err
		}
		if total == 0 {
			total = info.Total
		} else if info.Total != total {
			return "", fmt.Errorf("inconsistent chunk totals: %d vs %d", total, info.Total)
		}
		parts[info.Index] = info.Data
	}

	if int32(len(parts)) != total {
		return "", fmt.Errorf("incomplete: got %d of %d chunks", len(parts), total)
	}

	// Reassemble in order.
	var b strings.Builder
	for i := int32(1); i <= total; i++ {
		d, ok := parts[i]
		if !ok {
			return "", fmt.Errorf("missing chunk %d", i)
		}
		b.WriteString(d)
	}

	return DecryptProfile(b.String())
}
