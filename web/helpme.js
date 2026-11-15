// Guxos HelpMe — landing page and browser viewer (helper side).
// Session links are https://guxos.com/helpme/#<room>.<key>: the key stays in the fragment and is never sent
// to the server. Every message is encrypted/decrypted here with WebCrypto (AES-GCM).
(function () {
  "use strict";
  var MSG = {HELLO: 1, TILE: 2, FRAME_END: 3, CURSOR: 4, STATUS: 5, VIDEO_ON: 6, VIDEO: 7, VIDEO_OFF: 8, VIEW_HELLO: 10, MOUSE: 11, KEY: 12, ACK: 13, BYE: 14, TEXT: 15};
  var lang = (localStorage.getItem("helpme-lang") || navigator.language || "en").slice(0, 2) === "fr" ? "fr" : "en";
  var L = {
    en: {name: "Your first name", connecting: "Connecting…", waiting: "Waiting for the person to accept on their screen…",
      denied: "The person declined the request.", nosession: "This session no longer exists: ask the person to send a new link.",
      busy: "Someone is already connected to this session.", gone: "The session has ended.", live: "● Connected", badlink: "This link is incomplete or damaged.",
      lost: "Connection lost. Reconnecting…", nocrypto: "Your browser is too old (WebCrypto required). Please update it.", bye: "Session ended."},
    fr: {name: "Votre prénom", connecting: "Connexion…", waiting: "En attente de l'accord de la personne sur son écran…",
      denied: "La personne a refusé la demande.", nosession: "Cette session n'existe plus : demandez à la personne de vous envoyer un nouveau lien.",
      busy: "Quelqu'un est déjà connecté à cette session.", gone: "La session est terminée.", live: "● Connecté", badlink: "Ce lien est incomplet ou abîmé.",
      lost: "Connexion perdue. Reconnexion…", nocrypto: "Votre navigateur est trop ancien (WebCrypto nécessaire). Mettez-le à jour.", bye: "Session terminée."}
  };
  function T(k) { return L[lang][k]; }
  function applyLang() {
    document.documentElement.lang = lang;
    document.querySelectorAll("[data-en]").forEach(function (el) { el.textContent = el.getAttribute("data-" + lang); });
    document.getElementById("langbtn").textContent = lang === "fr" ? "EN · English" : "FR · Français";
    document.getElementById("name").placeholder = T("name");
  }
  document.getElementById("langbtn").addEventListener("click", function () {
    lang = lang === "fr" ? "en" : "fr";
    try { localStorage.setItem("helpme-lang", lang); } catch (e) {}
    applyLang();
  });
  applyLang();
  fetch("/helpme/HelpMe.exe.sha256").then(function (r) { return r.ok ? r.text() : "—"; })
    .then(function (s) { document.getElementById("sha").textContent = s.trim().split(/\s+/)[0]; }).catch(function () {});

  // ------------------------------------------------------------------ session
  var frag = location.hash.slice(1);
  if (!frag) return;
  var parts = frag.split(".");
  document.getElementById("site").style.display = "none";
  var join = document.getElementById("join");
  join.style.display = "block";
  var jstate = document.getElementById("jstate");
  function b64url(s) {
    s = s.replace(/-/g, "+").replace(/_/g, "/");
    while (s.length % 4) s += "=";
    var bin = atob(s), out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }
  var room, keyBytes;
  try {
    room = parts[0];
    keyBytes = b64url(parts[1]);
    if (parts.length !== 2 || room.length !== 12 || keyBytes.length !== 16) throw 0;
  } catch (e) { jstate.textContent = T("badlink"); document.getElementById("go").disabled = true; return; }
  if (!window.crypto || !crypto.subtle) { jstate.textContent = T("nocrypto"); return; }
  var nameIn = document.getElementById("name");
  try { nameIn.value = localStorage.getItem("helpme-name") || ""; } catch (e) {}

  var key, ws, queue = Promise.resolve(), pending = [], live = false, ended = false;
  var decoder = null, videoTs = 0, h264 = false;
  // H.264 support (WebCodecs): announced to the host, which only switches to video if we can decode it.
  if (window.VideoDecoder && VideoDecoder.isConfigSupported) {
    VideoDecoder.isConfigSupported({codec: "avc1.42E01F", optimizeForLatency: true})
      .then(function (r) { h264 = !!r.supported; }, function () {});
  }
  var canvas = document.getElementById("screen"), ctx = canvas.getContext("2d"), cur = document.getElementById("cur");
  var viewer = document.getElementById("viewer"), stateEl = document.getElementById("state"), stage = document.getElementById("stage");
  var enc = new TextEncoder(), dec = new TextDecoder();

  function seal(u8) {
    var iv = crypto.getRandomValues(new Uint8Array(12));
    return crypto.subtle.encrypt({name: "AES-GCM", iv: iv}, key, u8).then(function (ct) {
      var out = new Uint8Array(12 + ct.byteLength);
      out.set(iv);
      out.set(new Uint8Array(ct), 12);
      return out;
    });
  }
  function open(buf) {
    var u = new Uint8Array(buf);
    return crypto.subtle.decrypt({name: "AES-GCM", iv: u.subarray(0, 12)}, key, u.subarray(12)).then(function (p) { return new Uint8Array(p); });
  }
  function send(u8) {
    if (!ws || ws.readyState !== 1) return;
    seal(u8).then(function (m) { if (ws && ws.readyState === 1) ws.send(m); });
  }
  function sendJSON(type, obj) {
    var j = enc.encode(JSON.stringify(obj)), m = new Uint8Array(1 + j.length);
    m[0] = type; m.set(j, 1); send(m);
  }

  document.getElementById("go").addEventListener("click", start);
  nameIn.addEventListener("keydown", function (e) { if (e.key === "Enter") start(); });

  function start() {
    var name = nameIn.value.trim() || (lang === "fr" ? "Assistant" : "Helper");
    try { localStorage.setItem("helpme-name", name); } catch (e) {}
    document.getElementById("go").disabled = true;
    jstate.textContent = T("connecting");
    crypto.subtle.importKey("raw", keyBytes, "AES-GCM", false, ["encrypt", "decrypt"]).then(function (k) {
      key = k;
      connect(name);
    });
  }

  function connect(name) {
    var url = location.origin.replace(/^http/, "ws") + "/helpme/ws?role=view&room=" + encodeURIComponent(room);
    ws = new WebSocket(url);
    ws.binaryType = "arraybuffer";
    ws.onmessage = function (ev) {
      if (typeof ev.data === "string") {
        var m = {};
        try { m = JSON.parse(ev.data); } catch (e) {}
        if (m.t === "peer") { jstate.textContent = T("waiting"); sendJSON(MSG.VIEW_HELLO, {name: name, h264: h264}); }
        return;
      }
      var data = ev.data;
      queue = queue.then(function () { return open(data).then(handle, function () {}); });
    };
    ws.onclose = function (ev) {
      if (ended) return;
      var msg = ev.code === 4004 ? T("nosession") : ev.code === 4009 ? T("busy") : ev.code === 4000 ? T("gone") : T("gone");
      showEnd(msg);
    };
  }

  function showEnd(msg) {
    ended = true;
    live = false;
    viewer.style.display = "none";
    join.style.display = "block";
    jstate.textContent = msg;
    document.getElementById("go").style.display = "none";
    if (document.fullscreenElement) document.exitFullscreen().catch(function () {});
  }

  function handle(m) {
    var t = m[0], dv = new DataView(m.buffer, m.byteOffset, m.byteLength);
    if (t === MSG.STATUS) {
      var st = JSON.parse(dec.decode(m.subarray(1)));
      if (st.state === "accepted") {
        live = true;
        join.style.display = "none";
        viewer.style.display = "flex";
        stateEl.textContent = T("live");
        stage.focus();
      } else if (st.state === "denied") {
        showEnd(T("denied"));
        ws.close();
      }
    } else if (t === MSG.HELLO) {
      var h = JSON.parse(dec.decode(m.subarray(1)));
      canvas.width = h.w; canvas.height = h.h;
    } else if (t === MSG.TILE) {
      var x = dv.getUint16(1, true), y = dv.getUint16(3, true);
      var blob = new Blob([m.subarray(9)], {type: "image/jpeg"});
      pending.push(createImageBitmap(blob).then(function (bmp) { return {bmp: bmp, x: x, y: y}; }));
    } else if (t === MSG.VIDEO_ON) {
      var v = JSON.parse(dec.decode(m.subarray(1)));
      if (decoder) { try { decoder.close(); } catch (e) {} }
      decoder = new VideoDecoder({
        output: function (frame) { ctx.drawImage(frame, 0, 0); frame.close(); },
        error: function () { decoder = null; }
      });
      decoder.configure({codec: v.codec, codedWidth: v.w, codedHeight: v.h, optimizeForLatency: true});
      videoTs = 0;
    } else if (t === MSG.VIDEO) {
      if (decoder && decoder.state === "configured") {
        decoder.decode(new EncodedVideoChunk({type: m[1] ? "key" : "delta", timestamp: (videoTs += 50000), data: m.subarray(2)}));
      }
    } else if (t === MSG.VIDEO_OFF) {
      if (decoder) { try { decoder.close(); } catch (e) {} decoder = null; }  // drop late frames: a sharp full refresh follows
    } else if (t === MSG.FRAME_END) {
      var seq = dv.getUint32(1, true), tiles = pending;
      pending = [];
      if (decoder && decoder.decodeQueueSize > 2) {
        return new Promise(function (res) {
          var d = decoder;
          d.addEventListener("dequeue", function f() { if (d.decodeQueueSize <= 2) { d.removeEventListener("dequeue", f); res(); } });
          setTimeout(res, 1000);
        }).then(function () { var a = new Uint8Array(5); a[0] = MSG.ACK; new DataView(a.buffer).setUint32(1, seq, true); send(a); });
      }
      return Promise.all(tiles).then(function (list) {
        list.forEach(function (o) { ctx.drawImage(o.bmp, o.x, o.y); o.bmp.close && o.bmp.close(); });
        var a = new Uint8Array(5); a[0] = MSG.ACK; new DataView(a.buffer).setUint32(1, seq, true); send(a);
      }, function () {
        var a = new Uint8Array(5); a[0] = MSG.ACK; new DataView(a.buffer).setUint32(1, seq, true); send(a);
      });
    } else if (t === MSG.CURSOR) {
      var cx = dv.getUint16(1, true), cy = dv.getUint16(3, true);
      cur.style.display = "block";
      cur.style.left = (cx / canvas.width * 100) + "%";
      cur.style.top = (cy / canvas.height * 100) + "%";
    }
  }

  // ------------------------------------------------------------------ souris / toucher
  function pos(clientX, clientY) {
    var r = canvas.getBoundingClientRect();
    var x = Math.round((clientX - r.left) / r.width * canvas.width), y = Math.round((clientY - r.top) / r.height * canvas.height);
    return [Math.max(0, Math.min(canvas.width - 1, x)), Math.max(0, Math.min(canvas.height - 1, y))];
  }
  function mouse(action, p, delta) {
    if (!live) return;
    var m = new Uint8Array(8), dv = new DataView(m.buffer);
    m[0] = MSG.MOUSE; m[1] = action; dv.setUint16(2, p[0], true); dv.setUint16(4, p[1], true); dv.setInt16(6, delta || 0, true);
    send(m);
  }
  var lastMove = null, moveQueued = false;
  function queueMove(p) {
    lastMove = p;
    if (moveQueued) return;
    moveQueued = true;
    requestAnimationFrame(function () { moveQueued = false; mouse(0, lastMove); });
  }
  var BTN_DOWN = [1, 5, 3], BTN_UP = [2, 6, 4];
  canvas.addEventListener("mousemove", function (e) { queueMove(pos(e.clientX, e.clientY)); });
  canvas.addEventListener("mousedown", function (e) { e.preventDefault(); stage.focus(); if (e.button < 3) mouse(BTN_DOWN[e.button], pos(e.clientX, e.clientY)); });
  canvas.addEventListener("mouseup", function (e) { e.preventDefault(); if (e.button < 3) mouse(BTN_UP[e.button], pos(e.clientX, e.clientY)); });
  canvas.addEventListener("contextmenu", function (e) { e.preventDefault(); });
  canvas.addEventListener("wheel", function (e) { e.preventDefault(); mouse(7, pos(e.clientX, e.clientY), e.deltaY > 0 ? -120 : 120); }, {passive: false});
  canvas.addEventListener("touchstart", function (e) { e.preventDefault(); var t = e.touches[0], p = pos(t.clientX, t.clientY); mouse(0, p); mouse(1, p); lastMove = p; }, {passive: false});
  canvas.addEventListener("touchmove", function (e) { e.preventDefault(); var t = e.touches[0]; queueMove(pos(t.clientX, t.clientY)); }, {passive: false});
  canvas.addEventListener("touchend", function (e) { e.preventDefault(); mouse(2, lastMove || [0, 0]); }, {passive: false});

  // ------------------------------------------------------------------ clavier (codes de position, jeu 1)
  var SC = {Escape: 1, Digit1: 2, Digit2: 3, Digit3: 4, Digit4: 5, Digit5: 6, Digit6: 7, Digit7: 8, Digit8: 9, Digit9: 10, Digit0: 11,
    Minus: 12, Equal: 13, Backspace: 14, Tab: 15, KeyQ: 16, KeyW: 17, KeyE: 18, KeyR: 19, KeyT: 20, KeyY: 21, KeyU: 22, KeyI: 23, KeyO: 24,
    KeyP: 25, BracketLeft: 26, BracketRight: 27, Enter: 28, ControlLeft: 29, KeyA: 30, KeyS: 31, KeyD: 32, KeyF: 33, KeyG: 34, KeyH: 35,
    KeyJ: 36, KeyK: 37, KeyL: 38, Semicolon: 39, Quote: 40, Backquote: 41, ShiftLeft: 42, Backslash: 43, KeyZ: 44, KeyX: 45, KeyC: 46,
    KeyV: 47, KeyB: 48, KeyN: 49, KeyM: 50, Comma: 51, Period: 52, Slash: 53, ShiftRight: 54, NumpadMultiply: 55, AltLeft: 56, Space: 57,
    CapsLock: 58, F1: 59, F2: 60, F3: 61, F4: 62, F5: 63, F6: 64, F7: 65, F8: 66, F9: 67, F10: 68, NumLock: 69, ScrollLock: 70,
    Numpad7: 71, Numpad8: 72, Numpad9: 73, NumpadSubtract: 74, Numpad4: 75, Numpad5: 76, Numpad6: 77, NumpadAdd: 78, Numpad1: 79,
    Numpad2: 80, Numpad3: 81, Numpad0: 82, NumpadDecimal: 83, IntlBackslash: 86, F11: 87, F12: 88};
  var SCX = {NumpadEnter: 28, ControlRight: 29, NumpadDivide: 53, PrintScreen: 55, AltRight: 56, Home: 71, ArrowUp: 72, PageUp: 73,
    ArrowLeft: 75, ArrowRight: 77, End: 79, ArrowDown: 80, PageDown: 81, Insert: 82, Delete: 83, MetaLeft: 91, MetaRight: 92, ContextMenu: 93};
  function key(e, down) {
    if (!live || e.target === kbd) return;
    var sc = SC[e.code], ext = 0;
    if (sc === undefined) { sc = SCX[e.code]; ext = 1; }
    if (sc === undefined) return;
    e.preventDefault();
    var m = new Uint8Array(5);
    m[0] = MSG.KEY; m[1] = down ? 1 : 0; m[2] = sc & 255; m[3] = sc >> 8; m[4] = ext;
    send(m);
  }
  stage.addEventListener("keydown", function (e) { key(e, true); });
  stage.addEventListener("keyup", function (e) { key(e, false); });

  // Phone keyboards: hidden input, typed text is sent as Unicode characters.
  var kbd = document.getElementById("kbd");
  document.getElementById("kbbtn").addEventListener("click", function () { kbd.value = ""; kbd.focus(); });
  kbd.addEventListener("input", function () {
    if (!kbd.value) return;
    var t = enc.encode(kbd.value), m = new Uint8Array(1 + t.length);
    m[0] = MSG.TEXT; m.set(t, 1); send(m); kbd.value = "";
  });
  kbd.addEventListener("keydown", function (e) {
    var special = {Backspace: 14, Enter: 28, Tab: 15, Escape: 1};
    if (special[e.key] && live) {
      e.preventDefault();
      [1, 0].forEach(function (d) { var m = new Uint8Array(5); m[0] = MSG.KEY; m[1] = d; m[2] = special[e.key]; send(m); });
    }
  });

  document.getElementById("fsbtn").addEventListener("click", function () {
    if (document.fullscreenElement) document.exitFullscreen(); else viewer.requestFullscreen().catch(function () {});
    stage.focus();
  });
  document.getElementById("stop").addEventListener("click", function () {
    send(new Uint8Array([MSG.BYE]));
    setTimeout(function () { ended = true; if (ws) ws.close(); showEnd(T("bye")); }, 150);
  });
  window.addEventListener("beforeunload", function () { if (live) send(new Uint8Array([MSG.BYE])); });
})();
