// Command viewcheck is a command-line test viewer. It joins a HelpMe session like a browser would,
// waits for consent, rebuilds the received screen into a PNG and can nudge the mouse (-wiggle).
//
//	go run ./cmd/viewcheck -link "https://guxos.com/helpme/#room.key" -out screen.png -wiggle
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/webgisman/helpme/internal/proto"
	"github.com/webgisman/helpme/internal/spake2"
)

func main() {
	link := flag.String("link", "", "session link")
	code := flag.String("code", "", "9-digit short code (instead of -link)")
	out := flag.String("out", "screen.png", "output image")
	frames := flag.Int("frames", 12, "frames to receive")
	wiggle := flag.Bool("wiggle", false, "move the mouse in a small square at the centre")
	flag.Parse()
	base := "wss://guxos.com"
	var c *proto.Cipher
	query := ""
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, *code)
	if *code != "" {
		if len(digits) != 9 {
			fmt.Println("the code must have 9 digits")
			os.Exit(2)
		}
		query = "code=" + digits[:4]
	} else {
		i := strings.Index(*link, "#")
		if i < 0 {
			fmt.Println("link has no #")
			os.Exit(2)
		}
		room, key, err := proto.ParseFragment((*link)[i+1:])
		if err != nil {
			fmt.Println(err)
			os.Exit(2)
		}
		base = strings.Replace((*link)[:strings.Index(*link, "/helpme/")], "https://", "wss://", 1)
		c, _ = proto.NewCipher(key)
		query = "room=" + room
	}
	var pake *spake2.State
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, base+"/helpme/ws?role=view&"+query,
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://guxos.com"}}})
	if err != nil {
		fmt.Println("connect:", err)
		os.Exit(1)
	}
	conn.SetReadLimit(16 << 20)
	send := func(b []byte) { conn.Write(ctx, websocket.MessageBinary, c.Seal(b)) }
	var pendingKey, pendingExpected []byte
	sendHello := func() {
		hello, _ := json.Marshal(map[string]string{"name": "Claude (test)"})
		send(append([]byte{proto.MsgViewHello}, hello...))
		fmt.Println("request sent: waiting for consent…")
	}
	var canvas *image.RGBA
	got, tiles, bytesIn, cursors := 0, 0, 0, 0

	var accepted time.Time
	for got < *frames {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			fmt.Println("read:", err)
			break
		}
		if typ == websocket.MessageText {
			var p struct{ T, Mode, Room string }
			json.Unmarshal(data, &p)
			if p.T == "peer" && p.Mode == "code" {
				pake, _, _ = spake2.Start(spake2.B, p.Room, digits[4:])
				fmt.Println("connected with the short code, running SPAKE2…")
			} else if p.T == "peer" {
				sendHello()
			}
			continue
		}
		if c == nil && pake != nil && len(data) == 66 && data[0] == proto.PakeA {
			key, confirm, expected, err := pake.Finish(data[1:])
			if err != nil {
				fmt.Println("spake2:", err)
				os.Exit(1)
			}
			msgB := pake.Message()
			conn.Write(ctx, websocket.MessageBinary, append(append([]byte{proto.PakeB}, msgB...), confirm...))
			pendingKey, pendingExpected = key, expected
			continue
		}
		if c == nil && len(data) > 0 && data[0] == proto.PakeOK {
			if !hmac.Equal(data[1:], pendingExpected) {
				fmt.Println("host confirmation does not match!")
				os.Exit(1)
			}
			c, _ = proto.NewCipher(pendingKey)
			fmt.Println("SPAKE2 OK: session key agreed with the host")
			sendHello()
			continue
		}
		if c == nil && len(data) > 0 && data[0] == proto.PakeFail {
			fmt.Println("wrong code")
			os.Exit(1)
		}
		if c == nil {
			continue
		}
		bytesIn += len(data)
		m, err := c.Open(data)
		if err != nil {
			fmt.Println("undecryptable message!")
			continue
		}
		switch m[0] {
		case proto.MsgStatus:
			fmt.Println("status:", string(m[1:]))
			if strings.Contains(string(m), "accepted") {
				accepted = time.Now()
			} else {
				os.Exit(1)
			}
		case proto.MsgHello:
			var h struct{ W, H int }
			json.Unmarshal(m[1:], &h)
			canvas = image.NewRGBA(image.Rect(0, 0, h.W, h.H))
			fmt.Printf("remote screen: %dx%d\n", h.W, h.H)
		case proto.MsgTile:
			x, y := int(binary.LittleEndian.Uint16(m[1:])), int(binary.LittleEndian.Uint16(m[3:]))
			img, err := jpeg.Decode(bytes.NewReader(m[9:]))
			if err == nil && canvas != nil {
				draw.Draw(canvas, img.Bounds().Add(image.Pt(x, y)), img, image.Point{}, draw.Src)
				tiles++
			}
		case proto.MsgCursor:
			cursors++
		case proto.MsgFrameEnd:
			got++
			send(proto.U32(proto.MsgAck, binary.LittleEndian.Uint32(m[1:])))
			if *wiggle && got == 3 && canvas != nil {
				cx, cy := canvas.Bounds().Dx()/2, canvas.Bounds().Dy()/2
				for _, d := range [][2]int{{-40, -40}, {40, -40}, {40, 40}, {-40, 40}, {0, 0}} {
					mm := make([]byte, 8)
					mm[0], mm[1] = proto.MsgMouse, proto.MouseMove
					binary.LittleEndian.PutUint16(mm[2:], uint16(cx+d[0]))
					binary.LittleEndian.PutUint16(mm[4:], uint16(cy+d[1]))
					send(mm)
					time.Sleep(250 * time.Millisecond)
				}
				fmt.Println("mouse moved in a small square at the centre of the screen")
			}
		}
	}
	send([]byte{proto.MsgBye})
	conn.Close(websocket.StatusNormalClosure, "")
	if canvas != nil {
		f, _ := os.Create(*out)
		png.Encode(f, canvas)
		f.Close()
	}
	el := time.Since(accepted)
	fmt.Printf("%d frames, %d tiles, %d cursor updates, %d KB received in %.1f s after consent (%.1f fps) -> %s\n",
		got, tiles, cursors, bytesIn/1024, el.Seconds(), float64(got)/el.Seconds(), *out)
}
