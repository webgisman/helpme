// SPAKE2 (RFC 9382) over P-256, browser side (role B). Must match internal/spake2 exactly.
// Plain BigInt arithmetic: a handshake costs a few scalar multiplications (tens of ms).
(function (root) {
  "use strict";
  var P = 0xffffffff00000001000000000000000000000000ffffffffffffffffffffffffn;
  var N = 0xffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551n;
  var B = 0x5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604bn;
  var G = [0x6b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296n,
    0x4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5n];
  var M_HEX = "02886e2f97ace46e55ba9dd7242579f2993b64e16ef3dcab95afd497333d8fa12f";
  var N_HEX = "03d8bbd6c639c62937b04d997f38c3770719c629d7014d49a24b4f98baa1292b49";

  function mod(a, m) { a %= m; return a < 0n ? a + m : a; }
  function inv(a, m) {
    var t = 0n, nt = 1n, r = m, nr = mod(a, m);
    while (nr) { var q = r / nr; var tmp = t - q * nt; t = nt; nt = tmp; tmp = r - q * nr; r = nr; nr = tmp; }
    return mod(t, m);
  }
  function powmod(b, e, m) {
    var r = 1n; b = mod(b, m);
    while (e > 0n) { if (e & 1n) r = r * b % m; b = b * b % m; e >>= 1n; }
    return r;
  }
  // affine points, null = infinity
  function add(p, q) {
    if (!p) return q;
    if (!q) return p;
    var l;
    if (p[0] === q[0]) {
      if (mod(p[1] + q[1], P) === 0n) return null;
      l = mod((3n * p[0] * p[0] - 3n) * inv(2n * p[1], P), P);
    } else {
      l = mod((q[1] - p[1]) * inv(q[0] - p[0], P), P);
    }
    var x = mod(l * l - p[0] - q[0], P);
    return [x, mod(l * (p[0] - x) - p[1], P)];
  }
  function mul(p, k) {
    var r = null;
    k = mod(k, N);
    for (var i = 255; i >= 0; i--) {
      r = add(r, r);
      if ((k >> BigInt(i)) & 1n) r = add(r, p);
    }
    return r;
  }
  function hex(b) { return Array.from(b, function (x) { return x.toString(16).padStart(2, "0"); }).join(""); }
  function fromHex(h) { var o = new Uint8Array(h.length / 2); for (var i = 0; i < o.length; i++) o[i] = parseInt(h.substr(i * 2, 2), 16); return o; }
  function toBig(b) { return BigInt("0x" + (hex(b) || "0")); }
  function be32(v) { return fromHex(v.toString(16).padStart(64, "0")); }
  function decode(b) {
    var x, y;
    if (b.length === 65 && b[0] === 4) {
      x = toBig(b.subarray(1, 33)); y = toBig(b.subarray(33));
    } else if (b.length === 33 && (b[0] === 2 || b[0] === 3)) {
      x = toBig(b.subarray(1));
      y = powmod(mod(x * x * x - 3n * x + B, P), (P + 1n) / 4n, P);
      if (Number(y & 1n) !== (b[0] & 1)) y = P - y;
    } else throw new Error("bad point");
    if (x >= P || y >= P || mod(y * y - (x * x * x - 3n * x + B), P) !== 0n) throw new Error("point not on curve");
    return [x, y];
  }
  function encode(p) {
    if (!p) throw new Error("identity");
    var o = new Uint8Array(65); o[0] = 4; o.set(be32(p[0]), 1); o.set(be32(p[1]), 33); return o;
  }
  var enc = new TextEncoder();
  function lp(parts) {
    var len = 0; parts.forEach(function (p) { len += 8 + p.length; });
    var out = new Uint8Array(len), off = 0;
    parts.forEach(function (p) {
      var dv = new DataView(out.buffer, off, 8); dv.setUint32(0, p.length, true); dv.setUint32(4, 0, true);
      out.set(p, off + 8); off += 8 + p.length;
    });
    return out;
  }
  function sha256(b) { return crypto.subtle.digest("SHA-256", b).then(function (d) { return new Uint8Array(d); }); }
  function hmac(key, data) {
    return crypto.subtle.importKey("raw", key, {name: "HMAC", hash: "SHA-256"}, false, ["sign"])
      .then(function (k) { return crypto.subtle.sign("HMAC", k, data); }).then(function (s) { return new Uint8Array(s); });
  }
  function cat(a, b) { var o = new Uint8Array(a.length + b.length); o.set(a); o.set(b, a.length); return o; }

  // start(context, code, role) -> Promise<{msg, finish(peer) -> Promise<{key, confirm, expected}>}>
  function start(context, code, role) {
    role = role || "B";
    var x;
    do { x = mod(toBig(crypto.getRandomValues(new Uint8Array(48))), N); } while (x === 0n);
    return sha256(lp([enc.encode("helpme-spake2-w"), enc.encode(context), enc.encode(code)])).then(function (wh) {
      var w = mod(toBig(wh), N), wb = be32(w);
      var mine = decode(fromHex(role === "A" ? M_HEX : N_HEX)), theirs = decode(fromHex(role === "A" ? N_HEX : M_HEX));
      var msg = encode(add(mul(G, x), mul(mine, w)));
      return {
        msg: msg,
        finish: function (peer) {
          var Y = decode(peer);
          var K = mul(add(Y, mul(theirs, N - w)), x);
          var kb = encode(K);
          var pA = role === "A" ? msg : peer, pB = role === "A" ? peer : msg;
          var tt = lp([enc.encode("helpme-spake2-v1"), enc.encode(context), pA, pB, kb, wb]);
          return sha256(tt).then(function (h) {
            var d = function (label) { return sha256(cat(h, enc.encode(label))); };
            return Promise.all([d("key"), d("confirmA"), d("confirmB")]);
          }).then(function (k) {
            return Promise.all([hmac(k[1], tt), hmac(k[2], tt)]).then(function (c) {
              return {key: k[0].subarray(0, 16), confirm: role === "A" ? c[0] : c[1], expected: role === "A" ? c[1] : c[0]};
            });
          });
        }
      };
    });
  }
  root.HelpMeSpake2 = {start: start, hex: hex, fromHex: fromHex};
})(typeof window !== "undefined" ? window : globalThis);
