package xcheck

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"os/exec"
	"strings"
	"testing"

	"github.com/webgisman/helpme/internal/proto"
)

// A help request sealed by HelpMe.exe (Go) must open with WebCrypto using the helper's private key,
// and must not open without it.
func TestChannelInterop(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	room, _, _ := proto.NewSession()
	b64 := base64.RawURLEncoding
	id, pub, err := proto.ParseChannel(room + "." + b64.EncodeToString(priv.PublicKey().Bytes()))
	if err != nil || id != room {
		t.Fatal(err)
	}
	sealed, err := proto.SealToChannel(pub, []byte(`{"pc":"PC-01","code":"123456789"}`))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := proto.OpenFromChannel(priv, sealed); err != nil || !strings.Contains(string(plain), "PC-01") {
		t.Fatalf("go open: %v", err)
	}
	other, _ := ecdh.P256().GenerateKey(rand.Reader)
	if _, err := proto.OpenFromChannel(other, sealed); err == nil {
		t.Fatal("opened with the wrong private key")
	}
	pubRaw := priv.PublicKey().Bytes()
	js := `const [d,x,y,m]=process.argv.slice(1);const u=Uint8Array.from(Buffer.from(m,'hex'));(async()=>{
const priv=await crypto.subtle.importKey('jwk',{kty:'EC',crv:'P-256',d,x,y},{name:'ECDH',namedCurve:'P-256'},false,['deriveBits']);
const eph=await crypto.subtle.importKey('raw',u.subarray(0,65),{name:'ECDH',namedCurve:'P-256'},false,[]);
const shared=new Uint8Array(await crypto.subtle.deriveBits({name:'ECDH',public:eph},priv,256));
const lbl=new TextEncoder().encode('helpme-channel-v1');const kin=new Uint8Array(lbl.length+32);kin.set(lbl);kin.set(shared,lbl.length);
const k=new Uint8Array(await crypto.subtle.digest('SHA-256',kin)).subarray(0,16);
const key=await crypto.subtle.importKey('raw',k,'AES-GCM',false,['decrypt']);const body=u.subarray(65);
console.log(new TextDecoder().decode(await crypto.subtle.decrypt({name:'AES-GCM',iv:body.subarray(0,12)},key,body.subarray(12))));
})().catch(e=>{console.error(e);process.exit(1)});`
	out, err := exec.Command(node, "-e", js, b64.EncodeToString(priv.Bytes()), b64.EncodeToString(pubRaw[1:33]), b64.EncodeToString(pubRaw[33:]), hex.EncodeToString(sealed)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), `"code":"123456789"`) {
		t.Fatalf("webcrypto open: %v %s", err, out)
	}
}
