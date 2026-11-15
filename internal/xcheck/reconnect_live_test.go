package xcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/webgisman/helpme/internal/proto"
)

// Live check against the production relay (HELPME_LIVE=1): after an abrupt disconnect, a host that
// reconnects at once asking for the same short code should keep it.
func TestLiveReconnectKeepsCode(t *testing.T) {
	if os.Getenv("HELPME_LIVE") == "" {
		t.Skip("set HELPME_LIVE=1")
	}
	room, _, _ := proto.NewSession()
	dial := func(short string) (*websocket.Conn, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, "wss://guxos.com/helpme/ws?role=host&room="+room+"&short="+short, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Logf("first read: %v", err)
			return c, ""
		}
		var m struct{ Code string }
		json.Unmarshal(data, &m)
		return c, m.Code
	}
	c1, code := dial("")
	t.Logf("first code %s", code)
	c1.CloseNow() // abrupt
	for i := 0; i < 3; i++ {
		time.Sleep(2 * time.Second)
		c2, code2 := dial(code)
		t.Logf("reconnect %d: code %q", i, code2)
		if code2 != "" {
			c2.CloseNow()
			if code2 != code {
				t.Fatalf("code changed: %s -> %s", code, code2)
			}
			return
		}
		c2.CloseNow()
	}
	_ = http.StatusOK
}
