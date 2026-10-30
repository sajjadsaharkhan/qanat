package apikey

import (
	"bytes"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	k1, d1, h1 := New()
	k2, _, _ := New()
	if k1 == k2 {
		t.Fatal("keys must be random")
	}
	if !LooksValid(k1) || !strings.HasPrefix(d1, Prefix) || !strings.HasPrefix(k1, d1) {
		t.Fatalf("bad key %q display %q", k1, d1)
	}
	if !bytes.Equal(h1, Hash(k1)) {
		t.Fatal("hash mismatch")
	}
	if LooksValid("qnt_live_short") || LooksValid("sk-ant-"+k1[9:]) {
		t.Fatal("shape check too loose")
	}
}
