//go:build windows && amd64

// HelpMe.exe runs on the computer of the person being helped. No account, no install, no unattended
// access: it shows a session link (copied to the clipboard), asks for explicit consent on every
// connection, and the session ends when the window is closed.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/webgisman/helpme/internal/proto"
)

const version = "1.1.0"

var (
	relayURL = flag.String("relay", "wss://guxos.com/helpme/ws", "relay address")
	siteURL  = flag.String("site", "https://guxos.com/helpme/", "viewer page address")
	fr       bool
)

func t(frText, enText string) string {
	if fr {
		return frText
	}
	return enText
}

type host struct {
	conn   *websocket.Conn
	cipher *proto.Cipher
	wmu    sync.Mutex

	mu      sync.Mutex
	active  bool
	cancel  context.CancelFunc
	asking  bool
	acks    chan uint32
	screenW int
	screenH int
	h264OK  bool // the viewer can decode H.264 (WebCodecs)
}

func (h *host) send(plain []byte) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return h.conn.Write(ctx, websocket.MessageBinary, h.cipher.Seal(plain))
}

func (h *host) sendJSON(typ byte, v any) error {
	b, _ := json.Marshal(v)
	return h.send(append([]byte{typ}, b...))
}

func (h *host) stop(reason string) {
	h.mu.Lock()
	was := h.active
	if h.cancel != nil {
		h.cancel()
	}
	h.active, h.cancel = false, nil
	h.mu.Unlock()
	if was {
		setTitle("HelpMe")
		fmt.Println("\n  " + t("■ Session terminée", "■ Session ended") + " (" + reason + ").")
		fmt.Println("  " + t("Le même lien reste valable tant que cette fenêtre est ouverte.", "The same link stays valid while this window is open."))
	}
}

func cleanName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s))
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	if s == "" {
		s = t("Quelqu'un", "Someone")
	}
	return s
}

func (h *host) onViewHello(payload []byte) {
	var hello struct {
		Name string
		H264 bool
	}
	json.Unmarshal(payload, &hello)
	name := cleanName(hello.Name)
	h.mu.Lock()
	if h.active || h.asking {
		h.mu.Unlock()
		return
	}
	h.asking = true
	h.mu.Unlock()
	fmt.Printf("\n  %s\n", t("« "+name+" » demande à voir et contrôler votre écran…", "\""+name+"\" asks to view and control your screen…"))
	ok := askConsent("HelpMe",
		t("« "+name+" » demande à voir votre écran et à utiliser votre souris et votre clavier.\n\n"+
			"N'acceptez que si vous connaissez cette personne et que vous lui avez demandé de l'aide.\n"+
			"Vous pourrez tout arrêter à tout moment en fermant la fenêtre HelpMe.\n\nAccepter ?",
			"\""+name+"\" asks to see your screen and use your mouse and keyboard.\n\n"+
				"Only accept if you know this person and asked them for help.\n"+
				"You can stop everything at any time by closing the HelpMe window.\n\nAccept?"))
	h.mu.Lock()
	h.asking = false
	h.mu.Unlock()
	if !ok {
		fmt.Println("  " + t("✗ Refusé.", "✗ Refused."))
		h.sendJSON(proto.MsgStatus, map[string]string{"state": "denied"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.active, h.cancel, h.h264OK = true, cancel, hello.H264
	h.acks = make(chan uint32, 8)
	h.mu.Unlock()
	setTitle(t("● HelpMe — SESSION EN COURS (fermez pour arrêter)", "● HelpMe — SESSION ACTIVE (close to stop)"))
	fmt.Println("  " + t("● Session en cours avec « "+name+" ». Fermez cette fenêtre pour l'arrêter.",
		"● Session active with \""+name+"\". Close this window to stop it."))
	h.sendJSON(proto.MsgStatus, map[string]string{"state": "accepted"})
	go h.stream(ctx)
}

// Mode switching thresholds.
const (
	videoFPS      = 20
	motionHigh    = 0.20 // share of the screen that changed
	motionLow     = 0.02
	framesToVideo = 3
	framesToTiles = 30 // 1.5 s of calm at 20 fps
)

// stream sends the screen. Tiles mode sends only the changed areas as JPEG (sharp text, little data);
// when a large part of the screen keeps moving it switches to H.264 video and back again when things
// calm down. Each frame waits for the viewer's ack, so the rate adapts to the connection by itself.
func (h *host) stream(ctx context.Context) {
	runtime.LockOSThread() // the Media Foundation encoder lives on this thread
	defer runtime.UnlockOSThread()
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("  error:", r)
		}
	}()
	var (
		capt      *capturer
		enc       *h264Encoder
		prev      []byte
		seq       uint32
		quality   = 70
		hot, calm int
		videoOn   bool
		videoSent bool // MsgVideoOn already sent for the current encoder
		noVideo   bool // encoder unavailable on this machine
		lastCX    = -1
		lastCY    = -1
	)
	defer func() {
		if enc != nil {
			enc.close()
		}
		if capt != nil {
			capt.close()
		}
	}()
	h.mu.Lock()
	canVideo := h.h264OK
	h.mu.Unlock()

	stopVideo := func() {
		if enc != nil {
			enc.close()
			enc = nil
		}
		if videoOn && videoSent {
			h.send([]byte{proto.MsgVideoOff})
		}
		videoOn, videoSent, calm, hot = false, false, 0, 0
		prev = nil // full sharp refresh in tiles mode
	}

	for ctx.Err() == nil {
		start := time.Now()
		w, hh := screenSize()
		if capt == nil || w != capt.w || hh != capt.h {
			stopVideo()
			if capt != nil {
				capt.close()
			}
			var err error
			if capt, err = newCapturer(); err != nil {
				time.Sleep(time.Second)
				continue
			}
			h.mu.Lock()
			h.screenW, h.screenH = capt.w, capt.h
			h.mu.Unlock()
			if h.sendJSON(proto.MsgHello, map[string]any{"w": capt.w, "h": capt.h, "os": "windows", "ver": version}) != nil {
				return
			}
		}
		if capt.grab() != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		rects := changedRects(prev, capt.bits, capt.w, capt.h, prev == nil)
		changed := 0
		for _, r := range rects {
			changed += r.w * r.h
		}
		motion := float64(changed) / float64(capt.w*capt.h)

		if !videoOn && canVideo && !noVideo && prev != nil {
			if motion >= motionHigh {
				hot++
			} else {
				hot = 0
			}
			if hot >= framesToVideo {
				bitrate := min(6_000_000, max(1_500_000, capt.w*capt.h*2))
				var err error
				if enc, err = newH264Encoder(capt.w, capt.h, videoFPS, bitrate); err != nil {
					noVideo = true // e.g. Windows N without the Media Feature Pack: stay on tiles
				} else {
					videoOn, calm = true, 0
				}
			}
		} else if videoOn {
			if motion < motionLow {
				calm++
			} else {
				calm = 0
			}
			if calm >= framesToTiles {
				stopVideo()
				continue
			}
		}

		if videoOn {
			data, err := enc.encode(capt.bits, capt.w)
			if err != nil {
				noVideo = true
				stopVideo()
				continue
			}
			if len(data) > 0 {
				if !videoSent {
					codec := codecString(data)
					if codec == "" {
						codec = "avc1.42E01F"
					}
					if h.sendJSON(proto.MsgVideoOn, map[string]any{"w": enc.w, "h": enc.h, "codec": codec}) != nil {
						return
					}
					videoSent = true
				}
				key := byte(0)
				if isKeyframe(data) {
					key = 1
				}
				if h.send(append([]byte{proto.MsgVideo, key}, data...)) != nil {
					return
				}
			}
		} else {
			for _, r := range rects {
				jpg, err := encodeRect(capt.bits, capt.w, r, quality)
				if err != nil {
					continue
				}
				if h.send(proto.Tile(r.x, r.y, r.w, r.h, jpg)) != nil {
					return
				}
			}
		}
		if prev == nil {
			prev = make([]byte, len(capt.bits))
		}
		copy(prev, capt.bits)
		if cx, cy := cursorPos(); cx != lastCX || cy != lastCY {
			lastCX, lastCY = cx, cy
			h.send(proto.Cursor(max(0, cx), max(0, cy)))
		}
		seq++
		if h.send(proto.U32(proto.MsgFrameEnd, seq)) != nil {
			return
		}
		slow := !h.waitAck(ctx, seq, 3*time.Second)
		if ctx.Err() != nil {
			return
		}
		elapsed := time.Since(start)
		switch {
		case slow || elapsed > 700*time.Millisecond:
			quality = max(35, quality-10)
		case elapsed < 150*time.Millisecond && quality < 70:
			quality++
		}
		frameTime := 66 * time.Millisecond
		if videoOn {
			frameTime = time.Second / videoFPS
		} else if len(rects) == 0 {
			frameTime = 100 * time.Millisecond
		}
		if elapsed < frameTime {
			time.Sleep(frameTime - elapsed)
		}
	}
}

// waitAck waits for the viewer to acknowledge frame seq; false on timeout.
func (h *host) waitAck(ctx context.Context, seq uint32, timeout time.Duration) bool {
	h.mu.Lock()
	ch := h.acks
	h.mu.Unlock()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case a := <-ch:
			if a >= seq {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

func (h *host) onInput(msg []byte) {
	h.mu.Lock()
	active, w, hh := h.active, h.screenW, h.screenH
	h.mu.Unlock()
	if !active || w == 0 {
		return
	}
	switch msg[0] {
	case proto.MsgMouse:
		if len(msg) < 8 {
			return
		}
		x, y := int(binary.LittleEndian.Uint16(msg[2:])), int(binary.LittleEndian.Uint16(msg[4:]))
		delta := int16(binary.LittleEndian.Uint16(msg[6:]))
		injectMouse(int(msg[1]), min(x, w-1), min(y, hh-1), w, hh, delta)
	case proto.MsgKey:
		if len(msg) < 5 {
			return
		}
		injectKey(msg[1] == 1, binary.LittleEndian.Uint16(msg[2:]), msg[4] == 1)
	case proto.MsgText:
		if len(msg) > 1 && len(msg) < 2000 {
			injectText(string(msg[1:]))
		}
	}
}

func (h *host) run(ctx context.Context) error {
	for {
		typ, data, err := h.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageText {
			var m struct{ T string }
			json.Unmarshal(data, &m)
			if m.T == "gone" {
				h.stop(t("la personne s'est déconnectée", "the helper disconnected"))
			}
			continue
		}
		msg, err := h.cipher.Open(data)
		if err != nil || len(msg) == 0 {
			continue // wrong key or tampered message: ignored
		}
		switch msg[0] {
		case proto.MsgViewHello:
			go h.onViewHello(msg[1:])
		case proto.MsgAck:
			if len(msg) >= 5 {
				h.mu.Lock()
				ch := h.acks
				h.mu.Unlock()
				if ch != nil {
					select {
					case ch <- binary.LittleEndian.Uint32(msg[1:]):
					default:
					}
				}
			}
		case proto.MsgBye:
			h.stop(t("la personne a terminé", "the helper ended the session"))
		default:
			h.onInput(msg)
		}
	}
}

func main() {
	flag.Parse()
	setDPIAware()
	fr = isFrench()
	setTitle("HelpMe")
	room, key, err := proto.NewSession()
	if err != nil {
		fmt.Println(err)
		return
	}
	_, keyBytes, _ := proto.ParseFragment(room + "." + key)
	cph, _ := proto.NewCipher(keyBytes)
	link := *siteURL + "#" + room + "." + key

	fmt.Println()
	fmt.Println("  HelpMe " + version + " — Guxos")
	fmt.Println("  " + strings.Repeat("─", 60))
	fmt.Println("  " + t("1. Envoyez ce lien à la personne qui va vous aider :", "1. Send this link to the person who will help you:"))
	fmt.Println()
	fmt.Println("     " + link)
	fmt.Println()
	if copyToClipboard(link) {
		fmt.Println("  " + t("   (déjà copié : collez-le dans un SMS, un e-mail ou WhatsApp)", "   (already copied: paste it into a text, an email or WhatsApp)"))
	}
	fmt.Println("  " + t("2. Quand elle l'ouvre, Windows vous demande votre accord.", "2. When they open it, Windows asks for your permission."))
	fmt.Println("  " + t("3. Pour tout arrêter : fermez cette fenêtre.", "3. To stop everything: close this window."))
	fmt.Println()
	fmt.Println("  " + t("Chiffré de bout en bout : même notre serveur ne voit pas votre écran.", "End-to-end encrypted: not even our server can see your screen."))
	fmt.Println("  " + strings.Repeat("─", 60))

	busy := 0
	for attempt := 0; ; attempt++ {
		ctx := context.Background()
		dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		conn, _, err := websocket.Dial(dctx, *relayURL+"?role=host&room="+room, nil)
		cancel()
		if err != nil {
			if attempt == 0 {
				fmt.Println("  " + t("Connexion au serveur impossible, nouvel essai…", "Cannot reach the server, retrying…"))
			}
			time.Sleep(5 * time.Second)
			continue
		}
		if attempt > 0 {
			fmt.Println("  " + t("Connecté.", "Connected."))
		}
		conn.SetReadLimit(1 << 20)
		h := &host{conn: conn, cipher: cph}
		fmt.Println("\n  " + t("En attente de la personne qui vous aide…", "Waiting for the person helping you…"))
		err = h.run(ctx)
		h.stop(t("connexion perdue", "connection lost"))
		conn.CloseNow()
		if strings.Contains(fmt.Sprint(err), "4009") {
			// The room is still held by the relay (reconnecting too fast) or the relay is full: wait.
			busy++
			if busy > 6 {
				fmt.Println("  " + t("Session indisponible. Relancez HelpMe.", "Session unavailable. Please restart HelpMe."))
				fmt.Scanln()
				os.Exit(1)
			}
			time.Sleep(10 * time.Second)
			continue
		}
		busy, attempt = 0, 0
		time.Sleep(2 * time.Second)
	}
}
