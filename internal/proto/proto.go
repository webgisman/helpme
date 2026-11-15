// Package proto defines the HelpMe wire format and its end-to-end encryption.
//
// A session link looks like https://guxos.com/helpme/#<room>.<key>. The key (128 random bits) lives in
// the URL fragment, which browsers never send to a server, so the relay only ever sees the room id and
// ciphertext. Every message is AES-128-GCM with a random 12-byte nonce prepended.
package proto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

// Message types (first byte of the plaintext).
const (
	// host (person being helped) -> viewer
	MsgHello    = 1 // JSON {"w","h","os","ver"}
	MsgTile     = 2 // x,y,w,h (uint16 LE) + JPEG
	MsgFrameEnd = 3 // seq (uint32 LE)
	MsgCursor   = 4 // x,y (uint16 LE)
	MsgStatus   = 5 // JSON {"state": "accepted"|"denied"}
	MsgVideoOn  = 6 // JSON {"w","h","codec"}: H.264 mode starts
	MsgVideo    = 7 // keyframe u8 + H.264 Annex B access unit
	MsgVideoOff = 8 // back to tiles (a full tile refresh follows)

	// viewer (helper) -> host
	MsgViewHello = 10 // JSON {"name","h264"}
	MsgMouse     = 11 // action u8, x u16, y u16, wheel delta i16
	MsgKey       = 12 // down u8, scancode u16, extended u8
	MsgAck       = 13 // seq (uint32 LE)
	MsgBye       = 14 // the helper ends the session
	MsgText      = 15 // UTF-8 text (phone keyboards)
)

// Mouse actions (MsgMouse).
const (
	MouseMove = iota
	MouseLeftDown
	MouseLeftUp
	MouseRightDown
	MouseRightUp
	MouseMiddleDown
	MouseMiddleUp
	MouseWheel
)

const (
	RoomBytes = 9  // 12 base64url characters
	KeyBytes  = 16 // AES-128, 22 base64url characters
)

var b64 = base64.RawURLEncoding

// NewSession returns a random room id and key, base64url encoded.
func NewSession() (room, key string, err error) {
	r := make([]byte, RoomBytes)
	k := make([]byte, KeyBytes)
	if _, err = rand.Read(r); err != nil {
		return
	}
	if _, err = rand.Read(k); err != nil {
		return
	}
	return b64.EncodeToString(r), b64.EncodeToString(k), nil
}

// ParseFragment splits "<room>.<key>".
func ParseFragment(frag string) (room string, key []byte, err error) {
	frag = strings.TrimPrefix(frag, "#")
	parts := strings.Split(frag, ".")
	if len(parts) != 2 {
		return "", nil, errors.New("invalid link")
	}
	if r, e := b64.DecodeString(parts[0]); e != nil || len(r) != RoomBytes {
		return "", nil, errors.New("invalid room")
	}
	key, err = b64.DecodeString(parts[1])
	if err != nil || len(key) != KeyBytes {
		return "", nil, errors.New("invalid key")
	}
	return parts[0], key, nil
}

// ValidRoom checks the format of a room id (used by the relay).
func ValidRoom(room string) bool {
	r, err := b64.DecodeString(room)
	return err == nil && len(r) == RoomBytes && len(room) == 12
}

// Cipher seals and opens the messages of one session.
type Cipher struct{ aead cipher.AEAD }

func NewCipher(key []byte) (*Cipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead}, nil
}

// Seal returns nonce || ciphertext.
func (c *Cipher) Seal(plain []byte) []byte {
	out := make([]byte, 12, 12+len(plain)+16)
	if _, err := rand.Read(out); err != nil {
		panic(err)
	}
	return c.aead.Seal(out, out, plain, nil)
}

func (c *Cipher) Open(msg []byte) ([]byte, error) {
	if len(msg) < 12+16 {
		return nil, errors.New("message too short")
	}
	return c.aead.Open(nil, msg[:12], msg[12:], nil)
}

// Tile builds a MsgTile message.
func Tile(x, y, w, h int, jpeg []byte) []byte {
	b := make([]byte, 9, 9+len(jpeg))
	b[0] = MsgTile
	binary.LittleEndian.PutUint16(b[1:], uint16(x))
	binary.LittleEndian.PutUint16(b[3:], uint16(y))
	binary.LittleEndian.PutUint16(b[5:], uint16(w))
	binary.LittleEndian.PutUint16(b[7:], uint16(h))
	return append(b, jpeg...)
}

// U32 builds a message of type t followed by a uint32 (MsgFrameEnd, MsgAck).
func U32(t byte, v uint32) []byte {
	b := make([]byte, 5)
	b[0] = t
	binary.LittleEndian.PutUint32(b[1:], v)
	return b
}

func Cursor(x, y int) []byte {
	b := make([]byte, 5)
	b[0] = MsgCursor
	binary.LittleEndian.PutUint16(b[1:], uint16(x))
	binary.LittleEndian.PutUint16(b[3:], uint16(y))
	return b
}
