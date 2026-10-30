// Package apikey creates and hashes personal API keys (PRD §13.2).
//
// A key is "qnt_live_" followed by 43 base64url characters (32 random bytes).
// Only its SHA-256 is stored; the full key is shown to the user once. The
// prefix makes keys findable by secret scanners.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// Prefix marks every Qanat key.
const Prefix = "qnt_live_"

// displayLen is how much of the key is kept in clear for the Keys page.
const displayLen = len(Prefix) + 4

// New returns a fresh key, its display prefix and its hash.
func New() (key, display string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	key = Prefix + base64.RawURLEncoding.EncodeToString(b)
	return key, key[:displayLen], Hash(key)
}

// Hash is the stored form of a key.
func Hash(key string) []byte {
	h := sha256.Sum256([]byte(key))
	return h[:]
}

// LooksValid is a cheap shape check before any lookup.
func LooksValid(key string) bool {
	return strings.HasPrefix(key, Prefix) && len(key) == len(Prefix)+43
}
