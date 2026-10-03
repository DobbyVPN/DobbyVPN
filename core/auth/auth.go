package auth

import "crypto/rand"

// GenerateRandomAuth returns an independent credential with at least 128 bits
// of entropy, encoded using URL-safe characters for local SOCKS endpoints.
func GenerateRandomAuth() string { return rand.Text() }
