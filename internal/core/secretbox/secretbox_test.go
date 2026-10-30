package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func key(t *testing.T) string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func TestSealOpen(t *testing.T) {
	box, err := New(key(t))
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte(`{"access_token":"tok"}`)
	sealed, err := box.Seal(secret, []byte("acct-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, secret) {
		t.Fatal("plaintext visible in sealed value")
	}
	got, err := box.Open(sealed, []byte("acct-1"))
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := box.Open(sealed, []byte("acct-2")); err == nil {
		t.Fatal("value moved to another account must not open")
	}
	other, _ := New(key(t))
	if _, err := other.Open(sealed, []byte("acct-1")); err == nil {
		t.Fatal("another master key must not open")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := box.Open(sealed, []byte("acct-1")); err == nil {
		t.Fatal("tampering must be detected")
	}
}

func TestNew_RejectsBadKeys(t *testing.T) {
	for _, k := range []string{"", "short", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := New(k); err == nil {
			t.Errorf("%q accepted", k)
		}
	}
}
