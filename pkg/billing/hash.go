package billing

import (
	"crypto/sha256"
	"encoding/hex"
)

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
