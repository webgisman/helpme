package proto

import (
	"bytes"
	"testing"
)

func TestSessionAndCipher(t *testing.T) {
	room, key, err := NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidRoom(room) || len(room) != 12 {
		t.Fatalf("invalid room %q", room)
	}
	r, k, err := ParseFragment("#" + room + "." + key)
	if err != nil || r != room || len(k) != KeyBytes {
		t.Fatalf("ParseFragment: %v", err)
	}
	c, _ := NewCipher(k)
	msg := []byte("hello screen")
	sealed := c.Seal(msg)
	if bytes.Contains(sealed, msg) {
		t.Fatal("plaintext visible in ciphertext")
	}
	if out, err := c.Open(sealed); err != nil || !bytes.Equal(out, msg) {
		t.Fatalf("Open: %v", err)
	}
	if bytes.Equal(c.Seal(msg)[:12], sealed[:12]) {
		t.Fatal("nonce reused")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := c.Open(sealed); err == nil {
		t.Fatal("tampered message accepted")
	}
	_, other, _ := NewSession()
	_, k2, _ := ParseFragment(room + "." + other)
	c2, _ := NewCipher(k2)
	if _, err := c2.Open(c.Seal(msg)); err == nil {
		t.Fatal("wrong key accepted")
	}
}

func TestBadFragments(t *testing.T) {
	for _, f := range []string{"", "abc", "abc.def", "AAAAAAAAAAAA.short", "AAAAAAAAAAAA.AAAAAAAAAAAAAAAAAAAAAA.x"} {
		if _, _, err := ParseFragment(f); err == nil {
			t.Errorf("fragment accepted: %q", f)
		}
	}
	if ValidRoom("../../etc") || ValidRoom("") {
		t.Fatal("invalid room accepted")
	}
}
