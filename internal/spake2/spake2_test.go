package spake2

import (
	"bytes"
	"crypto/hmac"
	"testing"
)

func TestHandshake(t *testing.T) {
	a, ma, err := Start(A, "room1234", "56789")
	if err != nil {
		t.Fatal(err)
	}
	b, mb, _ := Start(B, "room1234", "56789")
	ka, ca, ea, err := a.Finish(mb)
	if err != nil {
		t.Fatal(err)
	}
	kb, cb, eb, err := b.Finish(ma)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ka, kb) || len(ka) != 16 {
		t.Fatal("keys differ")
	}
	if !hmac.Equal(ca, eb) || !hmac.Equal(cb, ea) {
		t.Fatal("confirmations do not match")
	}
}

func TestWrongCode(t *testing.T) {
	a, ma, _ := Start(A, "room1234", "56789")
	b, mb, _ := Start(B, "room1234", "56788")
	ka, _, ea, _ := a.Finish(mb)
	kb, cb, _, _ := b.Finish(ma)
	if bytes.Equal(ka, kb) || hmac.Equal(cb, ea) {
		t.Fatal("wrong code accepted")
	}
	// same code, different context (room) must not match either
	a2, ma2, _ := Start(A, "room9999", "56789")
	b2, mb2, _ := Start(B, "room1234", "56789")
	k1, _, _, _ := a2.Finish(mb2)
	k2, _, _, _ := b2.Finish(ma2)
	if bytes.Equal(k1, k2) {
		t.Fatal("context not bound")
	}
}

func TestInvalidPoint(t *testing.T) {
	a, _, _ := Start(A, "r", "1")
	if _, _, _, err := a.Finish([]byte{4, 1, 2, 3}); err == nil {
		t.Fatal("invalid point accepted")
	}
}
