package xcheck

import (
	"bytes"
	"crypto/hmac"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/webgisman/helpme/internal/spake2"
)

// The browser SPAKE2 (web/spake2.js, run in Node) must agree with the Go host on key and confirmations.
func TestSpake2Interop(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	js, _ := filepath.Abs("../../web/spake2.js")
	for _, tc := range []struct {
		hostCode, viewerCode string
		match                bool
	}{{"56789", "56789", true}, {"56789", "56780", false}} {
		a, ma, err := spake2.Start(spake2.A, "1234", tc.hostCode)
		if err != nil {
			t.Fatal(err)
		}
		script := `require(process.argv[1]);const S=globalThis.HelpMeSpake2;
S.start("1234",process.argv[2],"B").then(st=>st.finish(S.fromHex(process.argv[3])).then(r=>
console.log(JSON.stringify({msg:S.hex(st.msg),key:S.hex(r.key),confirm:S.hex(r.confirm),expected:S.hex(r.expected)}))))
.catch(e=>{console.error(e);process.exit(1)});`
		out, err := exec.Command(node, "-e", script, js, tc.viewerCode, hex.EncodeToString(ma)).CombinedOutput()
		if err != nil {
			t.Fatalf("node: %v %s", err, out)
		}
		var r struct{ Msg, Key, Confirm, Expected string }
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("%s", out)
		}
		mb, _ := hex.DecodeString(r.Msg)
		key, confirmA, expectedB, err := a.Finish(mb)
		if err != nil {
			t.Fatal(err)
		}
		jsKey, _ := hex.DecodeString(r.Key)
		jsConfirm, _ := hex.DecodeString(r.Confirm)
		jsExpected, _ := hex.DecodeString(r.Expected)
		same := bytes.Equal(key, jsKey) && hmac.Equal(expectedB, jsConfirm) && hmac.Equal(confirmA, jsExpected)
		if same != tc.match {
			t.Fatalf("codes %s/%s: match=%v, want %v", tc.hostCode, tc.viewerCode, same, tc.match)
		}
	}
}
