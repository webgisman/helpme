// Package spake2 implements SPAKE2 (RFC 9382) over P-256 with SHA-256, used to turn the short code a
// person reads out on the phone into a strong session key. A passive or active relay learns nothing
// that would let it test codes offline; every guess requires a live handshake with the host.
package spake2

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math/big"

	"filippo.io/nistec"
)

// M and N are the P-256 constants from RFC 9382, section 6.
var (
	pointM   = mustPoint("02886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f")
	pointN   = mustPoint("03d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49")
	order, _ = new(big.Int).SetString("ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551", 16)
)

func mustPoint(h string) []byte {
	b, _ := hex.DecodeString(h)
	if _, err := nistec.NewP256Point().SetBytes(b); err != nil {
		panic("spake2: bad constant")
	}
	return b
}

// Role: A is the host (person helped), B the viewer (helper).
type Role byte

const (
	A Role = 'A'
	B Role = 'B'
)

type State struct {
	role    Role
	context string
	x, w    []byte
	msg     []byte
}

func scalar(v *big.Int) []byte {
	b := make([]byte, 32)
	v.FillBytes(b)
	return b
}

// passwordScalar derives w from the code, bound to the session context.
func passwordScalar(context, code string) []byte {
	h := sha256.New()
	writeLP(h.Write, []byte("helpme-spake2-w"))
	writeLP(h.Write, []byte(context))
	writeLP(h.Write, []byte(code))
	w := new(big.Int).SetBytes(h.Sum(nil))
	return scalar(w.Mod(w, order))
}

func writeLP(write func([]byte) (int, error), b []byte) {
	var l [8]byte
	binary.LittleEndian.PutUint64(l[:], uint64(len(b)))
	write(l[:])
	write(b)
}

// Start returns the state and the first message (an uncompressed point) to send to the peer.
func Start(role Role, context, code string) (*State, []byte, error) {
	var x *big.Int
	for {
		buf := make([]byte, 48)
		if _, err := rand.Read(buf); err != nil {
			return nil, nil, err
		}
		x = new(big.Int).Mod(new(big.Int).SetBytes(buf), order)
		if x.Sign() != 0 {
			break
		}
	}
	s := &State{role: role, context: context, x: scalar(x), w: passwordScalar(context, code)}
	blind := pointM
	if role == B {
		blind = pointN
	}
	X, err := nistec.NewP256Point().ScalarBaseMult(s.x)
	if err != nil {
		return nil, nil, err
	}
	bp, _ := nistec.NewP256Point().SetBytes(blind)
	wB, err := nistec.NewP256Point().ScalarMult(bp, s.w)
	if err != nil {
		return nil, nil, err
	}
	s.msg = nistec.NewP256Point().Add(X, wB).Bytes()
	return s, s.msg, nil
}

// Finish processes the peer message. It returns the 16-byte session key, the confirmation to send and
// the confirmation expected from the peer (compare with hmac.Equal).
func (s *State) Finish(peer []byte) (key, confirm, expected []byte, err error) {
	Y, err := nistec.NewP256Point().SetBytes(peer)
	if err != nil {
		return nil, nil, nil, errors.New("spake2: invalid peer message")
	}
	blind := pointN
	if s.role == B {
		blind = pointM
	}
	bp, _ := nistec.NewP256Point().SetBytes(blind)
	negW := new(big.Int).Sub(order, new(big.Int).SetBytes(s.w))
	unblind, err := nistec.NewP256Point().ScalarMult(bp, scalar(negW.Mod(negW, order)))
	if err != nil {
		return nil, nil, nil, err
	}
	K, err := nistec.NewP256Point().ScalarMult(nistec.NewP256Point().Add(Y, unblind), s.x)
	if err != nil {
		return nil, nil, nil, err
	}
	kb := K.Bytes()
	if len(kb) == 1 {
		return nil, nil, nil, errors.New("spake2: identity point")
	}
	pA, pB := s.msg, peer
	if s.role == B {
		pA, pB = peer, s.msg
	}
	var tt []byte
	app := func(b []byte) (int, error) { tt = append(tt, b...); return len(b), nil }
	writeLP(app, []byte("helpme-spake2-v1"))
	writeLP(app, []byte(s.context))
	writeLP(app, pA)
	writeLP(app, pB)
	writeLP(app, kb)
	writeLP(app, s.w)
	h := sha256.Sum256(tt)
	derive := func(label string) []byte {
		d := sha256.Sum256(append(h[:], label...))
		return d[:]
	}
	mac := func(k []byte) []byte {
		m := hmac.New(sha256.New, k)
		m.Write(tt)
		return m.Sum(nil)
	}
	cA, cB := mac(derive("confirmA")), mac(derive("confirmB"))
	key = derive("key")[:16]
	if s.role == A {
		return key, cA, cB, nil
	}
	return key, cB, cA, nil
}

// Message returns the message produced by Start.
func (s *State) Message() []byte { return s.msg }
