// Command relay pairs the person being helped (host, HelpMe.exe) with the helper (browser) and carries
// help requests to helpers' inboxes.
//
// It only forwards end-to-end encrypted data: session keys never reach it (they are in URL fragments or
// derived with SPAKE2 from the short code), and help requests are encrypted with the channel key.
//
//	GET /helpme/ws?role=host&room=<room>[&short=<4 digits>]  HelpMe.exe; gets {"t":"code","code":"1234"}
//	GET /helpme/ws?role=view&room=<room> | &code=<4 digits>  browser (guxos.com origin only)
//	GET /helpme/ws?role=notify&ch=<channel>                  HelpMe.exe sends one encrypted help request
//	GET /helpme/ws?role=inbox&ch=<channel>                   helper's page receives the requests
//	GET /helpme/ws/health
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/webgisman/helpme/internal/proto"
)

const (
	maxRooms      = 5000
	maxRoomsPerIP = 10
	maxMessage    = 8 << 20
	roomLifetime  = 8 * time.Hour
	pingEvery     = 25 * time.Second

	requestTTL      = time.Hour
	maxQueued       = 20
	maxInboxPerChan = 5
	maxRequestSize  = 16 << 10
)

var shortRe = regexp.MustCompile(`^[0-9]{4}$`)

type room struct {
	id, short string
	host      *websocket.Conn
	view      *websocket.Conn
}

type request struct {
	at   time.Time
	data []byte
}

type channel struct {
	queue   []request
	inboxes map[*websocket.Conn]bool
}

type relay struct {
	mu       sync.Mutex
	rooms    map[string]*room
	shorts   map[string]*room
	perIP    map[string]int
	channels map[string]*channel
	origins  []string
	limits   *limiter
}

func newRelay(origins []string) *relay {
	return &relay{rooms: map[string]*room{}, shorts: map[string]*room{}, perIP: map[string]int{},
		channels: map[string]*channel{}, origins: origins, limits: newLimiter()}
}

// limiter is a small sliding-window counter keyed by string.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter() *limiter { return &limiter{hits: map[string][]time.Time{}} }

func (l *limiter) allow(key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) > window {
		i++
	}
	h = h[i:]
	if len(h) >= n {
		l.hits[key] = h
		return false
	}
	l.hits[key] = append(h, now)
	if len(l.hits) > 100000 {
		l.hits = map[string][]time.Time{}
	}
	return true
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func (s *relay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/health") {
		s.mu.Lock()
		body := fmt.Sprintf(`{"ok":true,"rooms":%d,"channels":%d}`, len(s.rooms), len(s.channels))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
		return
	}
	q := r.URL.Query()
	role, ip := q.Get("role"), clientIP(r)
	fromPage := r.Header.Get("Origin") != ""
	switch role {
	case "host", "notify":
		if fromPage { // HelpMe.exe never sends an Origin header
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	case "view", "inbox":
	default:
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if (role == "host" || role == "view") && q.Get("code") == "" && !proto.ValidRoom(q.Get("room")) ||
		(role == "notify" || role == "inbox") && !proto.ValidRoom(q.Get("ch")) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: s.origins, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	c.SetReadLimit(maxMessage)
	ctx := r.Context()
	switch role {
	case "host":
		s.serveHost(ctx, c, q.Get("room"), q.Get("short"), ip)
	case "view":
		s.serveView(ctx, c, q.Get("room"), q.Get("code"), ip)
	case "notify":
		s.serveNotify(ctx, c, q.Get("ch"), ip)
	case "inbox":
		s.serveInbox(ctx, c, q.Get("ch"))
	}
}

func writeText(ctx context.Context, c *websocket.Conn, v any) error {
	b, _ := json.Marshal(v)
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}

// pickShort returns a free 4-digit code, preferring the requested one. Caller holds s.mu.
func (s *relay) pickShort(want string) string {
	if shortRe.MatchString(want) && s.shorts[want] == nil {
		return want
	}
	for i := 0; i < 50; i++ {
		n, _ := rand.Int(rand.Reader, big.NewInt(9000))
		code := fmt.Sprintf("%04d", 1000+n.Int64())
		if s.shorts[code] == nil {
			return code
		}
	}
	return ""
}

func (s *relay) serveHost(ctx context.Context, c *websocket.Conn, id, want, ip string) {
	s.mu.Lock()
	if _, exists := s.rooms[id]; exists || len(s.rooms) >= maxRooms || s.perIP[ip] >= maxRoomsPerIP {
		s.mu.Unlock()
		c.Close(4009, "room unavailable")
		return
	}
	rm := &room{id: id, host: c, short: s.pickShort(want)}
	s.rooms[id] = rm
	if rm.short != "" {
		s.shorts[rm.short] = rm
	}
	s.perIP[ip]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.rooms, id)
		if rm.short != "" && s.shorts[rm.short] == rm {
			delete(s.shorts, rm.short)
		}
		if s.perIP[ip]--; s.perIP[ip] <= 0 {
			delete(s.perIP, ip)
		}
		v := rm.view
		s.mu.Unlock()
		if v != nil {
			v.Close(4000, "host left")
		}
		c.CloseNow()
	}()
	ctx, cancel := context.WithTimeout(ctx, roomLifetime)
	defer cancel()
	writeText(ctx, c, map[string]string{"t": "code", "code": rm.short})
	go keepAlive(ctx, c)
	for { // host -> current viewer
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			continue
		}
		s.mu.Lock()
		v := rm.view
		s.mu.Unlock()
		if v != nil {
			wctx, wcancel := context.WithTimeout(ctx, 20*time.Second)
			if v.Write(wctx, websocket.MessageBinary, data) != nil {
				v.Close(4001, "write failed")
			}
			wcancel()
		}
	}
}

func (s *relay) serveView(ctx context.Context, c *websocket.Conn, id, code, ip string) {
	mode := "link"
	if code != "" {
		mode = "code"
		// Short codes are guessable by design; SPAKE2 makes each guess an online attempt, and these
		// limits keep the number of attempts tiny.
		if !shortRe.MatchString(code) || !s.limits.allow("code-ip:"+ip, 20, 10*time.Minute) || !s.limits.allow("code:"+code, 10, 10*time.Minute) {
			c.Close(4029, "too many attempts")
			return
		}
	}
	s.mu.Lock()
	rm := s.rooms[id]
	if mode == "code" {
		rm = s.shorts[code]
	}
	if rm == nil || rm.view != nil {
		s.mu.Unlock()
		if rm == nil {
			c.Close(4004, "no such session")
		} else {
			c.Close(4009, "session busy")
		}
		return
	}
	rm.view = c
	host := rm.host
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if rm.view == c {
			rm.view = nil
		}
		s.mu.Unlock()
		writeText(context.Background(), host, map[string]string{"t": "gone"})
		c.CloseNow()
	}()
	writeText(ctx, host, map[string]string{"t": "peer", "mode": mode})
	writeText(ctx, c, map[string]string{"t": "peer", "mode": mode, "room": rm.id})
	go keepAlive(ctx, c)
	for { // viewer -> host
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary || len(data) > 2<<20 {
			continue // viewers send small messages (input, acks, clipboard text)
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		err = host.Write(wctx, websocket.MessageBinary, data)
		wcancel()
		if err != nil {
			return
		}
	}
}

func (s *relay) serveNotify(ctx context.Context, c *websocket.Conn, ch, ip string) {
	defer c.CloseNow()
	if !s.limits.allow("notify:"+ip, 30, time.Hour) {
		c.Close(4029, "too many requests")
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	typ, data, err := c.Read(rctx)
	if err != nil || typ != websocket.MessageBinary || len(data) > maxRequestSize {
		return
	}
	s.mu.Lock()
	chn := s.channels[ch]
	if chn == nil {
		if len(s.channels) >= 20000 {
			s.mu.Unlock()
			c.Close(4009, "busy")
			return
		}
		chn = &channel{inboxes: map[*websocket.Conn]bool{}}
		s.channels[ch] = chn
	}
	chn.queue = append(chn.queue, request{time.Now(), data})
	if len(chn.queue) > maxQueued {
		chn.queue = chn.queue[len(chn.queue)-maxQueued:]
	}
	var inboxes []*websocket.Conn
	for ib := range chn.inboxes {
		inboxes = append(inboxes, ib)
	}
	s.mu.Unlock()
	for _, ib := range inboxes {
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		ib.Write(wctx, websocket.MessageBinary, data)
		wcancel()
	}
	writeText(ctx, c, map[string]any{"t": "sent", "online": len(inboxes)})
	c.Close(websocket.StatusNormalClosure, "")
}

func (s *relay) serveInbox(ctx context.Context, c *websocket.Conn, ch string) {
	s.mu.Lock()
	chn := s.channels[ch]
	if chn == nil {
		chn = &channel{inboxes: map[*websocket.Conn]bool{}}
		s.channels[ch] = chn
	}
	if len(chn.inboxes) >= maxInboxPerChan {
		s.mu.Unlock()
		c.Close(4009, "too many inboxes")
		return
	}
	chn.inboxes[c] = true
	var pending [][]byte
	for _, r := range chn.queue {
		if time.Since(r.at) < requestTTL {
			pending = append(pending, r.data)
		}
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(chn.inboxes, c)
		s.mu.Unlock()
		c.CloseNow()
	}()
	for _, d := range pending {
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		c.Write(wctx, websocket.MessageBinary, d)
		cancel()
	}
	go keepAlive(ctx, c)
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
	}
}

// sweep drops expired help requests and empty channels.
func (s *relay) sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, chn := range s.channels {
		keep := chn.queue[:0]
		for _, r := range chn.queue {
			if time.Since(r.at) < requestTTL {
				keep = append(keep, r)
			}
		}
		chn.queue = keep
		if len(chn.queue) == 0 && len(chn.inboxes) == 0 {
			delete(s.channels, id)
		}
	}
}

func keepAlive(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err := c.Ping(pctx)
			cancel()
			if err != nil {
				c.CloseNow()
				return
			}
		}
	}
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	origins := flag.String("origins", "guxos.com,www.guxos.com", "allowed viewer origins")
	flag.Parse()
	s := newRelay(strings.Split(*origins, ","))
	go func() {
		for range time.Tick(time.Minute) {
			s.sweep()
		}
	}()
	srv := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("helpme relay listening on %s", *addr)
	log.Fatal(srv.ListenAndServe())
}
