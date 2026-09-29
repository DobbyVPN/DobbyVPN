package auth

import (
	"crypto/rand"
	"encoding/hex"
)

// Generate a secure random 8-character hex string
func GenerateRandomAuth() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
