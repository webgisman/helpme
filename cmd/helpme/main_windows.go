//go:build windows && amd64

// HelpMe.exe runs on the computer of the person being helped. No account, no install, no unattended
// access: it shows a short code and a link, asks for explicit consent on every connection, and the
// session ends when the window is closed or Stop is pressed.
package main

import (
	"context"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/coder/websocket"
	"github.com/webgisman/helpme/internal/proto"
	"github.com/webgisman/helpme/internal/spake2"
)

const version = "1.2.0"

var (
	relayURL   = flag.String("relay", "wss://guxos.com/helpme/ws", "relay address")
	siteURL    = flag.String("site", "https://guxos.com/helpme/", "viewer page address")
	channelArg = flag.String("channel", "", "help channel <id>.<public key> (from the helper's page)")
	helperArg  = flag.String("helper-name", "", "name of the helper shown on the Request help button")
	resumeArg  = flag.String("resume", "", "internal: <room>.<key> when relaunched as administrator")
	secretArg  = flag.String("secret", "", "internal: secret part of the short code")
	shortArg   = flag.String("short", "", "internal: short code to ask the relay for")
	fr         bool
)

func init() { runtime.LockOSThread() } // the window lives on the main thread

func t(frText, enText string) string {
	if fr {
		return frText
	}
	return enText
}

// app holds what survives reconnections: the session identity and the window.
type app struct {
	room, key, secret string
	linkCipher        *proto.Cipher
	ui                *gui

	mu       sync.Mutex
	short    string
	failures int
	cur      *host

	chanID  string
	chanPub *ecdh.PublicKey
	helper  string
}

func randomDigits(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		d, _ := rand.Int(rand.Reader, big.NewInt(10))
		b.WriteByte(byte('0' + d.Int64()))
	}
	return b.String()
}

func (a *app) link() string { return *siteURL + "#" + a.room + "." + a.key }

func (a *app) code() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.short == "" {
		return ""
	}
	c := a.short + a.secret
	return c[:3] + " " + c[3:6] + " " + c[6:]
}

func (a *app) refreshCode() {
	if c := a.code(); c != "" {
		a.ui.setText(a.ui.code, c)
	}
}

// host is one connection to the relay.
type host struct {
	app  *app
	conn *websocket.Conn
	wmu  sync.Mutex

	mu         sync.Mutex
	cipher     *proto.Cipher // per viewer: link key, or SPAKE2-derived key
	pake       *spake2.State
	active     bool
	cancel     context.CancelFunc
	asking     bool
	acks       chan uint32
	mon        monitor
	monIndex   int
	monChanged bool
	h264OK     bool
	clipIgnore uint32
}

func (h *host) sendRaw(b []byte) error {
	h.wmu.Lock()
	defer h.wmu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return h.conn.Write(ctx, websocket.MessageBinary, b)
}

func (h *host) send(plain []byte) error {
	h.mu.Lock()
	c := h.cipher
	h.mu.Unlock()
	if c == nil {
		return fmt.Errorf("no session key")
	}
	return h.sendRaw(c.Seal(plain))
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
		h.app.ui.setActive(false)
		h.app.ui.setStatus(stIdle, t("Session terminée ("+reason+").\nLe code reste valable tant que cette fenêtre est ouverte.",
			"Session ended ("+reason+").\nThe code stays valid while this window is open."))
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

// onPeer starts a viewer's handshake: link viewers already hold the key, code viewers run SPAKE2.
func (h *host) onPeer(mode string) {
	a := h.app
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pake = nil
	if mode != "code" {
		h.cipher = a.linkCipher
		return
	}
	h.cipher = nil
	st, msg, err := spake2.Start(spake2.A, a.room, a.secret)
	if err != nil {
		return
	}
	h.pake = st
	go h.sendRaw(append([]byte{proto.PakeA}, msg...))
}

func (h *host) onPakeB(m []byte) {
	a := h.app
	h.mu.Lock()
	st := h.pake
	h.pake = nil
	h.mu.Unlock()
	if st == nil || len(m) != 1+65+32 {
		return
	}
	key, confirm, expected, err := st.Finish(m[1:66])
	if err != nil || !hmac.Equal(expected, m[66:]) {
		h.sendRaw([]byte{proto.PakeFail})
		a.mu.Lock()
		a.failures++
		changed := a.failures >= 3
		if changed { // too many wrong codes: new secret, the old code is dead
			a.secret, a.failures = randomDigits(5), 0
		}
		a.mu.Unlock()
		if changed {
			a.refreshCode()
			a.ui.setStatus(stWarn, t("Plusieurs codes erronés ont été essayés : le code a été changé.",
				"Several wrong codes were tried: the code has been changed."))
		}
		return
	}
	c, _ := proto.NewCipher(key)
	h.mu.Lock()
	h.cipher = c
	h.mu.Unlock()
	a.mu.Lock()
	a.failures = 0
	a.mu.Unlock()
	h.sendRaw(append([]byte{proto.PakeOK}, confirm...))
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
	ui := h.app.ui
	ui.setStatus(stWarn, t("« "+name+" » demande à voir et contrôler votre écran…", "\""+name+"\" asks to view and control your screen…"))
	ok := askConsent("HelpMe",
		t("« "+name+" » demande à voir votre écran et à utiliser votre souris et votre clavier.\n\n"+
			"N'acceptez que si vous connaissez cette personne et que vous lui avez demandé de l'aide.\n"+
			"Vous pourrez tout arrêter à tout moment avec le bouton « Arrêter » ou en fermant HelpMe.\n\nAccepter ?",
			"\""+name+"\" asks to see your screen and use your mouse and keyboard.\n\n"+
				"Only accept if you know this person and asked them for help.\n"+
				"You can stop at any time with the Stop button or by closing HelpMe.\n\nAccept?"))
	h.mu.Lock()
	h.asking = false
	h.mu.Unlock()
	if !ok {
		ui.setStatus(stIdle, t("Demande refusée.", "Request declined."))
		h.sendJSON(proto.MsgStatus, map[string]string{"state": "denied"})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.mu.Lock()
	h.active, h.cancel, h.h264OK = true, cancel, hello.H264
	h.acks = make(chan uint32, 8)
	h.clipIgnore = clipboardSeq()
	h.mu.Unlock()
	setTitle(t("● HelpMe — SESSION EN COURS", "● HelpMe — SESSION ACTIVE"))
	ui.setActive(true)
	ui.setStatus(stActive, t("● Session en cours avec « "+name+" ».\nCette personne voit votre écran et peut l'utiliser.",
		"● Session active with \""+name+"\".\nThey can see and use your screen."))
	h.sendJSON(proto.MsgStatus, map[string]string{"state": "accepted"})
	go h.stream(ctx)
	go h.watchClipboard(ctx)
}

// watchClipboard sends text copied on this computer to the helper.
func (h *host) watchClipboard(ctx context.Context) {
	last := clipboardSeq()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		seq := clipboardSeq()
		if seq == last {
			continue
		}
		last = seq
		h.mu.Lock()
		ignore := h.clipIgnore
		h.mu.Unlock()
		if seq == ignore {
			continue // our own write from the helper's paste
		}
		if text := readClipboardText(256 << 10); text != "" {
			h.send(append([]byte{proto.MsgClipboard}, text...))
		}
	}
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
	defer func() { recover() }()
	var (
		capt      *capturer
		enc       *h264Encoder
		prev      []byte
		seq       uint32
		quality   = 70
		hot, calm int
		videoOn   bool
		videoSent bool
		noVideo   bool
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
		prev = nil
	}
	for ctx.Err() == nil {
		start := time.Now()
		mons := monitors()
		h.mu.Lock()
		idx, changed := h.monIndex, h.monChanged
		h.monChanged = false
		h.mu.Unlock()
		if idx >= len(mons) {
			idx = 0
		}
		m := mons[idx]
		if capt == nil || changed || m.X != capt.x || m.Y != capt.y || m.W != capt.w || m.H != capt.h {
			stopVideo()
			if capt != nil {
				capt.close()
			}
			var err error
			if capt, err = newCapturer(m); err != nil {
				time.Sleep(time.Second)
				continue
			}
			h.mu.Lock()
			h.mon = m
			h.mu.Unlock()
			if h.sendJSON(proto.MsgHello, map[string]any{"w": m.W, "h": m.H, "os": "windows", "ver": version,
				"monitors": mons, "monitor": idx, "admin": isAdmin()}) != nil {
				return
			}
		}
		if capt.grab() != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		rects := changedRects(prev, capt.bits, capt.w, capt.h, prev == nil)
		area := 0
		for _, r := range rects {
			area += r.w * r.h
		}
		motion := float64(area) / float64(capt.w*capt.h)
		if !videoOn && canVideo && !noVideo && prev != nil {
			if motion >= motionHigh {
				hot++
			} else {
				hot = 0
			}
			if hot >= framesToVideo {
				var err error
				if enc, err = newH264Encoder(capt.w, capt.h, videoFPS, min(6_000_000, max(1_500_000, capt.w*capt.h*2))); err != nil {
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
		cx, cy := cursorPos()
		cx, cy = cx-m.X, cy-m.Y
		if cx < 0 || cy < 0 || cx >= m.W || cy >= m.H {
			cx, cy = 65535, 65535 // on another display: hidden
		}
		if cx != lastCX || cy != lastCY {
			lastCX, lastCY = cx, cy
			h.send(proto.Cursor(cx, cy))
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

func (h *host) onMessage(msg []byte) {
	h.mu.Lock()
	active, m := h.active, h.mon
	h.mu.Unlock()
	switch msg[0] {
	case proto.MsgViewHello:
		go h.onViewHello(msg[1:])
		return
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
		return
	case proto.MsgBye:
		h.stop(t("la personne a terminé", "the helper ended the session"))
		return
	}
	if !active || m.W == 0 {
		return
	}
	switch msg[0] {
	case proto.MsgMouse:
		if len(msg) >= 8 {
			x, y := int(binary.LittleEndian.Uint16(msg[2:])), int(binary.LittleEndian.Uint16(msg[4:]))
			injectMouse(int(msg[1]), min(x, m.W-1), min(y, m.H-1), m, int16(binary.LittleEndian.Uint16(msg[6:])))
		}
	case proto.MsgKey:
		if len(msg) >= 5 {
			injectKey(msg[1] == 1, binary.LittleEndian.Uint16(msg[2:]), msg[4] == 1)
		}
	case proto.MsgText:
		if len(msg) > 1 && len(msg) < 2000 {
			injectText(string(msg[1:]))
		}
	case proto.MsgClipboard:
		if len(msg) > 1 && len(msg) < 1<<20 {
			copyToClipboard(string(msg[1:]))
			h.mu.Lock()
			h.clipIgnore = clipboardSeq()
			h.mu.Unlock()
		}
	case proto.MsgMonitor:
		if len(msg) >= 2 {
			h.mu.Lock()
			h.monIndex, h.monChanged = int(msg[1]), true
			h.mu.Unlock()
		}
	}
}

func (h *host) run(ctx context.Context) error {
	a := h.app
	for {
		typ, data, err := h.conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageText {
			var m struct{ T, Code, Mode string }
			json.Unmarshal(data, &m)
			switch m.T {
			case "code":
				a.mu.Lock()
				a.short = m.Code
				a.mu.Unlock()
				a.refreshCode()
			case "peer":
				h.onPeer(m.Mode)
			case "gone":
				h.stop(t("la personne s'est déconnectée", "the helper disconnected"))
				h.mu.Lock()
				h.cipher, h.pake = nil, nil
				h.mu.Unlock()
			}
			continue
		}
		if len(data) > 0 && data[0] == proto.PakeB {
			h.onPakeB(data)
			continue
		}
		h.mu.Lock()
		c := h.cipher
		h.mu.Unlock()
		if c == nil {
			continue
		}
		if msg, err := c.Open(data); err == nil && len(msg) > 0 {
			h.onMessage(msg)
		}
	}
}

func (a *app) connectLoop() {
	busy := 0
	for attempt := 0; ; attempt++ {
		a.mu.Lock()
		short := a.short
		if short == "" {
			short = *shortArg
		}
		a.mu.Unlock()
		dctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		conn, _, err := websocket.Dial(dctx, *relayURL+"?role=host&room="+a.room+"&short="+short, nil)
		cancel()
		if err != nil {
			if attempt == 0 {
				a.ui.setStatus(stWarn, t("Connexion au serveur impossible, nouvel essai…", "Cannot reach the server, retrying…"))
			}
			time.Sleep(5 * time.Second)
			continue
		}
		conn.SetReadLimit(1 << 20)
		h := &host{app: a, conn: conn}
		a.mu.Lock()
		a.cur = h
		a.mu.Unlock()
		a.ui.setStatus(stIdle, t("En attente de la personne qui vous aide…", "Waiting for the person helping you…"))
		err = h.run(context.Background())
		h.stop(t("connexion perdue", "connection lost"))
		conn.CloseNow()
		if strings.Contains(fmt.Sprint(err), "4009") {
			if busy++; busy > 6 {
				a.ui.setStatus(stWarn, t("Session indisponible. Relancez HelpMe.", "Session unavailable. Please restart HelpMe."))
				return
			}
			time.Sleep(10 * time.Second)
			continue
		}
		busy, attempt = 0, 0
		time.Sleep(2 * time.Second)
	}
}

// requestHelp sends the code and link, encrypted with the channel key, to the helper's inbox.
func (a *app) requestHelp() {
	a.ui.enableRequest(false)
	defer func() { time.Sleep(3 * time.Second); a.ui.enableRequest(true) }()
	payload, _ := json.Marshal(map[string]any{"pc": computerName(), "user": userName(), "code": strings.ReplaceAll(a.code(), " ", ""),
		"link": a.link(), "ver": version, "ts": time.Now().Unix()})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, *relayURL+"?role=notify&ch="+a.chanID, nil)
	if err == nil {
		defer conn.CloseNow()
		var sealed []byte
		if sealed, err = proto.SealToChannel(a.chanPub, payload); err == nil {
			err = conn.Write(ctx, websocket.MessageBinary, sealed)
		}
	}
	var ack struct{ Online int }
	if err == nil {
		var data []byte
		if _, data, err = conn.Read(ctx); err == nil {
			json.Unmarshal(data, &ack)
		}
	}
	switch {
	case err != nil:
		a.ui.setStatus(stWarn, t("La demande n'a pas pu être envoyée. Donnez le code par téléphone.", "The request could not be sent. Read the code out instead."))
	case ack.Online > 0:
		a.ui.setStatus(stIdle, t("✓ Demande envoyée à "+a.helper+". Restez près de votre ordinateur.", "✓ Request sent to "+a.helper+". Please stay near your computer."))
	default:
		a.ui.setStatus(stIdle, t("✓ Demande envoyée. "+a.helper+" la verra en ouvrant sa page HelpMe (valable 1 h).",
			"✓ Request sent. "+a.helper+" will see it when they open their HelpMe page (valid 1 h)."))
	}
}

var channelInName = regexp.MustCompile(`HelpMe[_-]([A-Za-z0-9_-]{12})\.([A-Za-z0-9_-]{87})`)

// loadChannel reads the help channel from -channel, helpme.json next to the exe, or the exe file name
// (personal download links are named HelpMe_<id>.<public key>.exe).
func (a *app) loadChannel() {
	exe, _ := os.Executable()
	ch, name := *channelArg, *helperArg
	if ch == "" {
		var cfg struct{ Channel, HelperName string }
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), "helpme.json")); err == nil && json.Unmarshal(b, &cfg) == nil {
			ch, name = cfg.Channel, cfg.HelperName
		}
	}
	if ch == "" {
		if m := channelInName.FindStringSubmatch(filepath.Base(exe)); m != nil {
			ch = m[1] + "." + m[2]
		}
	}
	id, pub, err := proto.ParseChannel(ch)
	if err != nil {
		return
	}
	a.chanID, a.chanPub = id, pub
	a.helper = strings.TrimSpace(name)
	if a.helper == "" {
		a.helper = t("la personne qui vous aide", "your helper")
	}
}

func mustCipher(k []byte) *proto.Cipher {
	c, _ := proto.NewCipher(k)
	return c
}

func main() {
	flag.Parse()
	setDPIAware()
	fr = isFrench()
	a := &app{}
	if _, _, err := proto.ParseFragment(*resumeArg); err == nil && regexp.MustCompile(`^\d{5}$`).MatchString(*secretArg) {
		parts := strings.SplitN(*resumeArg, ".", 2)
		a.room, a.key, a.secret = parts[0], parts[1], *secretArg
	} else {
		a.room, a.key, _ = proto.NewSession()
		a.secret = randomDigits(5)
	}
	_, keyBytes, _ := proto.ParseFragment(a.room + "." + a.key)
	a.linkCipher = mustCipher(keyBytes)
	a.loadChannel()

	title := "HelpMe " + version
	if isAdmin() {
		title += t(" (administrateur)", " (administrator)")
	}
	reqLabel := t("Demander de l'aide à ", "Request help from ") + a.helper
	a.ui = newGUI(title, a.chanPub != nil, map[string]string{
		"give_code": t("Donnez ce code à la personne qui vous aide :", "Give this code to the person helping you:"),
		"or_link":   t("ou envoyez-lui ce lien :", "or send them this link:"),
		"copy":      t("Copier le lien", "Copy link"),
		"email":     t("Envoyer par e-mail", "Email link"),
		"request":   reqLabel,
		"waiting":   t("Connexion…", "Connecting…"),
		"admin":     t("Relancer en administrateur", "Run as administrator"),
		"stop":      t("Arrêter la session", "Stop session"),
		"footer":    t("Chiffré de bout en bout · guxos.com/helpme", "End-to-end encrypted · guxos.com/helpme"),
	})
	a.ui.setText(a.ui.link, a.link())
	if isAdmin() {
		a.ui.hideAdmin()
	}
	a.ui.onCopy = func() {
		copyToClipboard(a.link())
		a.ui.setStatus(stIdle, t("Lien copié : collez-le dans un message (Ctrl+V).", "Link copied: paste it into a message (Ctrl+V)."))
	}
	a.ui.onEmail = func() {
		subject := t("Pouvez-vous m'aider sur mon ordinateur ?", "Can you help me with my computer?")
		body := t("Ouvrez ce lien pour voir mon écran : ", "Open this link to see my screen: ") + a.link() + "\n" +
			t("ou saisissez le code ", "or enter the code ") + a.code() + t(" sur ", " at ") + *siteURL
		shellExecute("open", "mailto:?subject="+url.PathEscape(subject)+"&body="+url.PathEscape(body), "")
	}
	a.ui.onRequest = a.requestHelp
	a.ui.onStop = func() {
		a.mu.Lock()
		h := a.cur
		a.mu.Unlock()
		if h != nil {
			h.sendJSON(proto.MsgStatus, map[string]string{"state": "stopped"})
			h.stop(t("arrêtée par vous", "stopped by you"))
			h.conn.CloseNow() // the helper is disconnected; the code stays valid
		}
	}
	a.ui.onAdmin = func() {
		exe, _ := os.Executable()
		a.mu.Lock()
		args := fmt.Sprintf("-resume %s.%s -secret %s -short %s", a.room, a.key, a.secret, a.short)
		a.mu.Unlock()
		if *channelArg != "" {
			args += " -channel " + *channelArg
		}
		if *helperArg != "" {
			args += ` -helper-name "` + strings.ReplaceAll(*helperArg, `"`, "") + `"`
		}
		a.mu.Lock()
		if h := a.cur; h != nil {
			h.conn.CloseNow() // free the room before the elevated copy takes it over
		}
		a.mu.Unlock()
		if shellExecute("runas", exe, args) {
			os.Exit(0)
		}
		a.ui.setStatus(stWarn, t("Élévation refusée.", "Elevation was declined."))
	}
	go a.connectLoop()
	a.ui.run()
	os.Exit(0)
}
