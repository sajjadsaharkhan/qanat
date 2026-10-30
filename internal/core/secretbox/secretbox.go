// Package secretbox is envelope encryption for upstream credentials at rest
// (PRD §15.2). Each value gets a fresh data key; the data key is sealed with
// the master key (a Docker secret). Rotating the master key only re-seals
// data keys.
//
// Format: version(1) | nonceK(12) | sealed data key(32+16) | nonceD(12) | sealed value.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const version = 1

// Box seals and opens values with one master key.
type Box struct{ master cipher.AEAD }

// New parses a base64 master key of 32 bytes.
func New(masterB64 string) (*Box, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(masterB64))
	if err != nil || len(k) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, base64-encoded (openssl rand -base64 32)")
	}
	aead, err := gcm(k)
	if err != nil {
		return nil, err
	}
	return &Box{master: aead}, nil
}

func gcm(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

// Seal encrypts plaintext. aad binds the ciphertext to its owner (e.g. the
// account ID), so a sealed value can't be moved to another row.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	dk := random(32)
	data, err := gcm(dk)
	if err != nil {
		return nil, err
	}
	nk, nd := random(12), random(12)
	out := []byte{version}
	out = append(out, nk...)
	out = b.master.Seal(out, nk, dk, aad)
	out = append(out, nd...)
	return data.Seal(out, nd, plaintext, aad), nil
}

var errCorrupt = errors.New("secretbox: value is corrupt or was sealed with another key")

// Open decrypts a value produced by Seal with the same aad.
func (b *Box) Open(sealed, aad []byte) ([]byte, error) {
	const keyLen = 12 + 32 + 16
	if len(sealed) < 1+keyLen+12+16 || sealed[0] != version {
		return nil, errCorrupt
	}
	nk, sk := sealed[1:13], sealed[13:1+keyLen]
	dk, err := b.master.Open(nil, nk, sk, aad)
	if err != nil {
		return nil, errCorrupt
	}
	data, err := gcm(dk)
	if err != nil {
		return nil, err
	}
	rest := sealed[1+keyLen:]
	out, err := data.Open(nil, rest[:12], rest[12:], aad)
	if err != nil {
		return nil, errCorrupt
	}
	return out, nil
}
