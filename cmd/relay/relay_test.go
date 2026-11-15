package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/webgisman/helpme/internal/proto"
)

func dial(t *testing.T, srv *httptest.Server, role, room string, hdr http.Header) (*websocket.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/helpme/ws?role=" + role + "&room=" + room
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	return c, err
}

func read(t *testing.T, c *websocket.Conn) (websocket.MessageType, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return typ, string(data)
}

func write(t *testing.T, c *websocket.Conn, s string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, []byte(s)); err != nil {
		t.Fatal(err)
	}
}

func TestRelayPairing(t *testing.T) {
	srv := httptest.NewServer(newRelay([]string{"guxos.com"}))
	defer srv.Close()
	room, _, _ := proto.NewSession()

	// a web page cannot pose as the host
	if _, err := dial(t, srv, "host", room, http.Header{"Origin": {"https://evil.example"}}); err == nil {
		t.Fatal("host with Origin header accepted")
	}
	// viewer without a host
	v0, err := dial(t, srv, "view", room, nil)
	if err == nil {
		_, _, rerr := v0.Read(context.Background())
		if websocket.CloseStatus(rerr) != 4004 {
			t.Fatalf("unknown room: code %v", websocket.CloseStatus(rerr))
		}
	}

	host, err := dial(t, srv, "host", room, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	if _, s := read(t, host); !strings.HasPrefix(s, `{"code":"`) {
		t.Fatalf("host should get its short code first: %q", s)
	}
	// same room, second host: refused
	h2, err := dial(t, srv, "host", room, nil)
	if err == nil {
		_, _, rerr := h2.Read(context.Background())
		if websocket.CloseStatus(rerr) != 4009 {
			t.Fatalf("second host: code %v", websocket.CloseStatus(rerr))
		}
	}
	// foreign origin for the viewer: refused
	if _, err := dial(t, srv, "view", room, http.Header{"Origin": {"https://evil.example"}}); err == nil {
		t.Fatal("viewer from foreign origin accepted")
	}

	view, err := dial(t, srv, "view", room, http.Header{"Origin": {"https://guxos.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, s := read(t, host); !strings.Contains(s, `"t":"peer"`) || !strings.Contains(s, `"mode":"link"`) {
		t.Fatalf("host: %q", s)
	}
	if _, s := read(t, view); !strings.Contains(s, `"t":"peer"`) {
		t.Fatalf("viewer: %q", s)
	}
	write(t, host, "screen")
	if typ, s := read(t, view); typ != websocket.MessageBinary || s != "screen" {
		t.Fatalf("host -> viewer: %q", s)
	}
	write(t, view, "mouse")
	if _, s := read(t, host); s != "mouse" {
		t.Fatalf("viewer -> host: %q", s)
	}
	// one viewer at a time
	v2, err := dial(t, srv, "view", room, http.Header{"Origin": {"https://guxos.com"}})
	if err == nil {
		_, _, rerr := v2.Read(context.Background())
		if websocket.CloseStatus(rerr) != 4009 {
			t.Fatalf("second viewer: code %v", websocket.CloseStatus(rerr))
		}
	}
	// viewer leaves: the host is told and the room accepts a new viewer
	view.Close(websocket.StatusNormalClosure, "")
	if _, s := read(t, host); s != `{"t":"gone"}` {
		t.Fatalf("leave: %q", s)
	}
	v3, err := dial(t, srv, "view", room, http.Header{"Origin": {"https://guxos.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, s := read(t, v3); !strings.Contains(s, `"t":"peer"`) {
		t.Fatalf("new viewer: %q", s)
	}
	// host leaves: the room is gone
	host.Close(websocket.StatusNormalClosure, "")
	_, _, rerr := v3.Read(context.Background())
	if websocket.CloseStatus(rerr) != 4000 {
		t.Fatalf("host leave: code %v", websocket.CloseStatus(rerr))
	}
}

func dialQ(t *testing.T, srv *httptest.Server, query string, hdr http.Header) (*websocket.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/helpme/ws?"+query, &websocket.DialOptions{HTTPHeader: hdr})
	return c, err
}

func TestShortCode(t *testing.T) {
	srv := httptest.NewServer(newRelay([]string{"guxos.com"}))
	defer srv.Close()
	room, _, _ := proto.NewSession()
	host, err := dialQ(t, srv, "role=host&room="+room+"&short=4321", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer host.CloseNow()
	if _, s := read(t, host); s != `{"code":"4321","t":"code"}` {
		t.Fatalf("requested short code not honoured: %q", s)
	}
	origin := http.Header{"Origin": {"https://guxos.com"}}
	view, err := dialQ(t, srv, "role=view&code=4321", origin)
	if err != nil {
		t.Fatal(err)
	}
	if _, s := read(t, host); !strings.Contains(s, `"mode":"code"`) {
		t.Fatalf("host should know the viewer came with a code: %q", s)
	}
	if _, s := read(t, view); !strings.Contains(s, room) {
		t.Fatalf("viewer should learn the room id: %q", s)
	}
	view.Close(websocket.StatusNormalClosure, "")
	read(t, host) // gone
	// brute force: the code is locked after a few attempts
	locked := false
	for i := 0; i < 15; i++ {
		v, err := dialQ(t, srv, "role=view&code=4321", origin)
		if err != nil {
			continue
		}
		_, _, rerr := v.Read(context.Background())
		if websocket.CloseStatus(rerr) == 4029 {
			locked = true
			break
		}
		v.CloseNow()
	}
	if !locked {
		t.Fatal("short code not rate limited")
	}
}

func TestHelpChannel(t *testing.T) {
	srv := httptest.NewServer(newRelay([]string{"guxos.com"}))
	defer srv.Close()
	ch, _, _ := proto.NewSession()
	// a request sent while the helper is offline waits in the queue
	n, err := dialQ(t, srv, "role=notify&ch="+ch, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, n, "request-1")
	if _, s := read(t, n); !strings.Contains(s, `"online":0`) {
		t.Fatalf("notify ack: %q", s)
	}
	if _, err := dialQ(t, srv, "role=notify&ch="+ch, http.Header{"Origin": {"https://guxos.com"}}); err == nil {
		t.Fatal("notify from a web page accepted")
	}
	inbox, err := dialQ(t, srv, "role=inbox&ch="+ch, http.Header{"Origin": {"https://guxos.com"}})
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.CloseNow()
	if _, s := read(t, inbox); s != "request-1" {
		t.Fatalf("queued request: %q", s)
	}
	// live delivery
	n2, _ := dialQ(t, srv, "role=notify&ch="+ch, nil)
	write(t, n2, "request-2")
	if _, s := read(t, inbox); s != "request-2" {
		t.Fatalf("live request: %q", s)
	}
}
