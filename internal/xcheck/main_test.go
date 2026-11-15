package xcheck

import (
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/webgisman/helpme/internal/proto"
)

// Go <-> WebCrypto interoperability (Node.js has the same API as browsers). Skipped without node.
func TestWebCryptoInterop(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	room, key, _ := proto.NewSession()
	_, k, _ := proto.ParseFragment(room + "." + key)
	c, _ := proto.NewCipher(k)
	sealed := hex.EncodeToString(c.Seal([]byte("from Go")))
	js := `const [k,s]=process.argv.slice(1);const b=x=>Uint8Array.from(Buffer.from(x.replace(/-/g,'+').replace(/_/g,'/'),'base64'));
(async()=>{const key=await crypto.subtle.importKey('raw',b(k),'AES-GCM',false,['encrypt','decrypt']);const u=Uint8Array.from(Buffer.from(s,'hex'));
const p=new TextDecoder().decode(await crypto.subtle.decrypt({name:'AES-GCM',iv:u.subarray(0,12)},key,u.subarray(12)));
const iv=crypto.getRandomValues(new Uint8Array(12));const ct=new Uint8Array(await crypto.subtle.encrypt({name:'AES-GCM',iv},key,new TextEncoder().encode(p+' and the browser')));
const o=new Uint8Array(12+ct.length);o.set(iv);o.set(ct,12);console.log(Buffer.from(o).toString('hex'));})().catch(e=>{console.error(e);process.exit(1)});`
	out, err := exec.Command(node, "-e", js, key, sealed).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
	back, _ := hex.DecodeString(strings.TrimSpace(string(out)))
	plain, err := c.Open(back)
	if err != nil || string(plain) != "from Go and the browser" {
		t.Fatalf("round trip: %q %v", plain, err)
	}
	_ = base64.RawURLEncoding
	_ = os.Stdout
}
