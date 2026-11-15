// Command relay pairs the person being helped (host, HelpMe.exe) with the helper (browser).
//
// It forwards end-to-end encrypted messages it cannot read (the key never reaches it) and keeps nothing
// but counters. One room = one host and at most one viewer at a time.
//
//	GET /helpme/ws?role=host&room=<room>   (HelpMe.exe)
//	GET /helpme/ws?role=view&room=<room>   (browser, https://guxos.com origin only)
//	GET /helpme/ws/health
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/webgisman/helpme/internal/proto"
)

const (
	maxRooms      = 2000
	maxRoomsPerIP = 10
	maxMessage    = 8 << 20
	roomLifetime  = 8 * time.Hour
	pingEvery     = 25 * time.Second
)

type room struct {
	host    *websocket.Conn
	view    *websocket.Conn
	ip      string
	created time.Time
}

type relay struct {
	mu      sync.Mutex
	rooms   map[string]*room
	perIP   map[string]int
	origins []string
}

func newRelay(origins []string) *relay {
	return &relay{rooms: map[string]*room{}, perIP: map[string]int{}, origins: origins}
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
		n := len(s.rooms)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"rooms":` + itoa(n) + `}`))
		return
	}
	roomID, role := r.URL.Query().Get("room"), r.URL.Query().Get("role")
	if !proto.ValidRoom(roomID) || (role != "host" && role != "view") {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	opts := &websocket.AcceptOptions{OriginPatterns: s.origins, CompressionMode: websocket.CompressionDisabled}
	if role == "host" && r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden) // hosts are HelpMe.exe, never a web page
		return
	}
	c, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}
	c.SetReadLimit(maxMessage)
	if role == "host" {
		s.serveHost(r.Context(), c, roomID, clientIP(r))
	} else {
		s.serveView(r.Context(), c, roomID)
	}
}

func (s *relay) serveHost(ctx context.Context, c *websocket.Conn, id, ip string) {
	s.mu.Lock()
	if _, exists := s.rooms[id]; exists || len(s.rooms) >= maxRooms || s.perIP[ip] >= maxRoomsPerIP {
		s.mu.Unlock()
		c.Close(4009, "room unavailable")
		return
	}
	rm := &room{host: c, ip: ip, created: time.Now()}
	s.rooms[id] = rm
	s.perIP[ip]++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.rooms, id)
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
	go keepAlive(ctx, c)
	// host -> current viewer
	for {
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

func (s *relay) serveView(ctx context.Context, c *websocket.Conn, id string) {
	s.mu.Lock()
	rm := s.rooms[id]
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
		hctx, hcancel := context.WithTimeout(context.Background(), 5*time.Second)
		host.Write(hctx, websocket.MessageText, []byte(`{"t":"gone"}`))
		hcancel()
		c.CloseNow()
	}()
	for _, conn := range []*websocket.Conn{host, c} {
		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		conn.Write(wctx, websocket.MessageText, []byte(`{"t":"peer"}`))
		wcancel()
	}
	go keepAlive(ctx, c)
	// viewer -> host
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary || len(data) > 1<<16 {
			continue // viewers only send small messages (mouse, keyboard, acks)
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		err = host.Write(wctx, websocket.MessageBinary, data)
		wcancel()
		if err != nil {
			return
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
			if err != nil && !errors.Is(err, context.Canceled) {
				c.CloseNow()
				return
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	origins := flag.String("origins", "guxos.com,www.guxos.com", "allowed viewer origins")
	flag.Parse()
	s := newRelay(strings.Split(*origins, ","))
	srv := &http.Server{Addr: *addr, Handler: s, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("helpme relay listening on %s", *addr)
	log.Fatal(srv.ListenAndServe())
}
