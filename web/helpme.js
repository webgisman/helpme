// Guxos HelpMe — browser side: landing page, short-code and link viewer, help channels and inbox.
// Session keys never leave the browser: links carry the key in the URL fragment, codes derive it with
// SPAKE2 (spake2.js). Help requests are encrypted to the channel's public key and opened here.
(function () {
  "use strict";
  var MSG = {HELLO: 1, TILE: 2, FRAME_END: 3, CURSOR: 4, STATUS: 5, VIDEO_ON: 6, VIDEO: 7, VIDEO_OFF: 8, VIEW_HELLO: 10,
    MOUSE: 11, KEY: 12, ACK: 13, BYE: 14, TEXT: 15, CLIPBOARD: 16, MONITOR: 17, PAKE_A: 0xF1, PAKE_B: 0xF2, PAKE_OK: 0xF3, PAKE_FAIL: 0xF4};
  var lang = "en";
  try { lang = (localStorage.getItem("helpme-lang") || navigator.language || "en").slice(0, 2) === "fr" ? "fr" : "en"; } catch (e) {}
  var L = {
    en: {name: "Your first name", connecting: "Connecting…", waiting: "Waiting for the person to accept on their screen…",
      denied: "The person declined the request.", nosession: "No HelpMe window is open with this code or link. Check it, or ask for a new one.",
      busy: "Someone is already connected to this session.", gone: "The session has ended.", live: "● Connected", badlink: "This link is incomplete or damaged.",
      nocrypto: "Your browser is too old (WebCrypto required). Please update it.", bye: "Session ended.", badcode: "Enter the 9 digits shown in HelpMe.",
      wrongcode: "Wrong code. After 3 wrong attempts the code is changed on their side.", toomany: "Too many attempts. Wait a few minutes.",
      restarting: "The person's HelpMe is restarting (e.g. as administrator), reconnecting…", display: "Display",
      notadmin: "Windows does not let you control administrator windows. Ask the person to click “Run as administrator” in HelpMe.",
      clipgot: "Their clipboard was copied to yours.", clipready: "Their clipboard is ready: click 📋 to copy it.", pasted: "Pasted on their computer.",
      inboxTitle: function (n) { return "Help requests — " + n; }, online: "● Listening", offline: "Reconnecting…",
      needs: function (u, pc) { return u + " (" + pc + ") needs help"; }, connect: "Connect", ago: " ago",
      getTitle: function (n) { return n + " can help you with HelpMe"; },
      openInbox: "Open my inbox", yourCh: "Your channels:", enterName: "Enter the name they will see."},
    fr: {name: "Votre prénom", connecting: "Connexion…", waiting: "En attente de l'accord de la personne sur son écran…",
      denied: "La personne a refusé la demande.", nosession: "Aucune fenêtre HelpMe n'est ouverte avec ce code ou ce lien. Vérifiez-le, ou demandez-en un nouveau.",
      busy: "Quelqu'un est déjà connecté à cette session.", gone: "La session est terminée.", live: "● Connecté", badlink: "Ce lien est incomplet ou abîmé.",
      nocrypto: "Votre navigateur est trop ancien (WebCrypto nécessaire). Mettez-le à jour.", bye: "Session terminée.", badcode: "Saisissez les 9 chiffres affichés dans HelpMe.",
      wrongcode: "Code incorrect. Après 3 essais faux, le code change de son côté.", toomany: "Trop d'essais. Patientez quelques minutes.",
      restarting: "HelpMe redémarre chez la personne (par ex. en administrateur), reconnexion…", display: "Écran",
      notadmin: "Windows ne vous laisse pas contrôler les fenêtres administrateur. Demandez à la personne de cliquer sur « Relancer en administrateur » dans HelpMe.",
      clipgot: "Son presse-papiers a été copié dans le vôtre.", clipready: "Son presse-papiers est prêt : cliquez sur 📋 pour le copier.", pasted: "Collé sur son ordinateur.",
      inboxTitle: function (n) { return "Demandes d'aide — " + n; }, online: "● À l'écoute", offline: "Reconnexion…",
      needs: function (u, pc) { return u + " (" + pc + ") demande de l'aide"; }, connect: "Se connecter", ago: "",
      getTitle: function (n) { return n + " peut vous aider avec HelpMe"; },
      openInbox: "Ouvrir ma boîte de réception", yourCh: "Vos canaux :", enterName: "Saisissez le nom qu'ils verront."}
  };
  function T(k) { return L[lang][k]; }
  function $(id) { return document.getElementById(id); }
  function show(id) { ["site", "get", "inbox", "join", "viewer"].forEach(function (v) { $(v).classList.toggle("hide", v !== id); }); }
  function applyLang() {
    document.documentElement.lang = lang;
    document.querySelectorAll("[data-en]").forEach(function (el) { el.textContent = el.getAttribute("data-" + lang); });
    document.querySelectorAll("[data-ph-en]").forEach(function (el) { el.placeholder = el.getAttribute("data-ph-" + lang); });
    document.querySelectorAll(".lang").forEach(function (b) { b.textContent = lang === "fr" ? "EN · English" : "FR · Français"; });
    $("name").placeholder = T("name");
  }
  document.querySelectorAll(".lang").forEach(function (b) {
    b.addEventListener("click", function () {
      lang = lang === "fr" ? "en" : "fr";
      try { localStorage.setItem("helpme-lang", lang); } catch (e) {}
      applyLang();
      renderChannels();
    });
  });

  var b64 = {
    enc: function (u8) { var s = ""; u8.forEach(function (c) { s += String.fromCharCode(c); }); return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""); },
    dec: function (s) { s = s.replace(/-/g, "+").replace(/_/g, "/"); while (s.length % 4) s += "="; var bin = atob(s), o = new Uint8Array(bin.length); for (var i = 0; i < o.length; i++) o[i] = bin.charCodeAt(i); return o; }
  };
  var enc = new TextEncoder(), dec = new TextDecoder();
  function esc(s) { var d = document.createElement("div"); d.textContent = s; return d.innerHTML; }

  // ------------------------------------------------------------------ routing
  var hash = location.hash.slice(1);
  var params = {};
  hash.split("&").forEach(function (kv) { var i = kv.indexOf("="); if (i > 0) params[kv.slice(0, i)] = decodeURIComponent(kv.slice(i + 1)); });
  applyLang();
  fetch("/helpme/HelpMe.exe.sha256").then(function (r) { return r.ok ? r.text() : "—"; })
    .then(function (s) { document.querySelectorAll(".sha").forEach(function (e) { e.textContent = s.trim().split(/\s+/)[0]; }); }).catch(function () {});
  if (!window.crypto || !crypto.subtle) { show("site"); $("codemsg").textContent = T("nocrypto"); return; }

  // ------------------------------------------------------------------ landing: code + channels
  function channels() { try { return JSON.parse(localStorage.getItem("helpme-channels") || "[]"); } catch (e) { return []; } }
  function inboxURL(c) { return location.origin + "/helpme/#inbox=" + c.id + "." + c.d + "." + c.pub + "&n=" + encodeURIComponent(c.name); }
  function renderChannels() {
    var list = channels(), box = $("mychannels");
    if (!list.length) { box.innerHTML = ""; return; }
    box.innerHTML = "<p class='muted'>" + esc(T("yourCh")) + "</p>" + list.map(function (c) {
      return "<p><a class='btn small ghost' href='" + esc(inboxURL(c)) + "'>" + esc(T("openInbox")) + " — " + esc(c.name) + "</a></p>";
    }).join("");
  }
  $("codego").addEventListener("click", function () {
    var digits = $("codein").value.replace(/\D/g, "");
    if (digits.length !== 9) { $("codemsg").textContent = T("badcode"); return; }
    startJoin({mode: "code", code: digits});
  });
  $("codein").addEventListener("keydown", function (e) { if (e.key === "Enter") $("codego").click(); });
  $("codein").addEventListener("input", function () {
    var d = this.value.replace(/\D/g, "").slice(0, 9);
    this.value = d.replace(/(\d{3})(?=\d)/g, "$1 ");
  });
  $("chcreate").addEventListener("click", function () {
    var name = $("chname").value.trim();
    if (!name) { $("chres").textContent = T("enterName"); return; }
    crypto.subtle.generateKey({name: "ECDH", namedCurve: "P-256"}, true, ["deriveBits"]).then(function (kp) {
      return Promise.all([crypto.subtle.exportKey("jwk", kp.privateKey), crypto.subtle.exportKey("raw", kp.publicKey)]);
    }).then(function (r) {
      var c = {id: b64.enc(crypto.getRandomValues(new Uint8Array(9))), d: r[0].d, pub: b64.enc(new Uint8Array(r[1])), name: name};
      var list = channels(); list.push(c);
      try { localStorage.setItem("helpme-channels", JSON.stringify(list)); } catch (e) {}
      location.href = inboxURL(c);
      location.reload();
    });
  });

  // ------------------------------------------------------------------ viewer state
  var target, key, ws, queue = Promise.resolve(), pending = [], live = false, ended = false, wasLive = false;
  var decoder = null, videoTs = 0, h264 = false, pake = null, myName = "", reconnectUntil = 0, remoteClip = "";
  var canvas = $("screen"), ctx = canvas.getContext("2d"), cur = $("cur"), stage = $("stage"), kbd = $("kbd");
  if (window.VideoDecoder && VideoDecoder.isConfigSupported) {
    VideoDecoder.isConfigSupported({codec: "avc1.42E01F", optimizeForLatency: true}).then(function (r) { h264 = !!r.supported; }, function () {});
  }

  window.addEventListener("hashchange", function () { location.reload(); }); // pasting another link in the same tab
  if (params.inbox) startInbox(params.inbox);
  else if (params.get) startGet(params.get, params.n || "HelpMe");
  else if (hash && hash.indexOf("=") < 0) startJoin({mode: "link", frag: hash});
  else { show("site"); renderChannels(); }

  // ------------------------------------------------------------------ personal download page
  function startGet(ch, name) {
    show("get");
    $("gettitle").textContent = L[lang].getTitle(name);
    var parts = ch.split(".");
    if (parts.length === 2 && parts[0].length === 12 && parts[1].length === 87) {
      $("getdl").setAttribute("download", "HelpMe_" + parts[0] + "." + parts[1] + ".exe");
    }
  }

  // ------------------------------------------------------------------ helper inbox
  function startInbox(v) {
    show("inbox");
    var p = v.split("."), name = params.n || "HelpMe";
    if (p.length !== 3) { $("inboxstate").textContent = T("badlink"); return; }
    var id = p[0], d = p[1], pubRaw = b64.dec(p[2]);
    $("inboxtitle").textContent = L[lang].inboxTitle(name);
    $("sharelink").textContent = location.origin + "/helpme/#get=" + id + "." + p[2] + "&n=" + encodeURIComponent(name);
    $("cfgjson").textContent = JSON.stringify({channel: id + "." + p[2], helper_name: name}, null, 2);
    var jwk = {kty: "EC", crv: "P-256", d: d, x: b64.enc(pubRaw.subarray(1, 33)), y: b64.enc(pubRaw.subarray(33)), ext: true};
    var known = [];
    try { known = JSON.parse(sessionStorage.getItem("helpme-req-" + id) || "[]"); } catch (e) {}
    function save() { try { sessionStorage.setItem("helpme-req-" + id, JSON.stringify(known)); } catch (e) {} }
    function render(fresh) {
      known = known.filter(function (r) { return Date.now() / 1000 - r.ts < 3600; });
      $("noreq").classList.toggle("hide", known.length > 0);
      $("reqs").innerHTML = known.slice().reverse().map(function (r, i) {
        var mins = Math.max(0, Math.round((Date.now() / 1000 - r.ts) / 60));
        var codeTxt = r.code ? String(r.code).replace(/(\d{3})(\d{3})(\d{3})/, "$1 $2 $3") : "";
        return "<div class='req" + (i === 0 && fresh ? " new" : "") + "'><div><strong>" + esc(L[lang].needs(r.user || "?", r.pc || "?")) +
          "</strong><br><span class='muted'>" + mins + " min" + esc(T("ago")) + (codeTxt ? " · code " + esc(codeTxt) : "") +
          "</span></div><a class='btn small' target='_blank' rel='noopener' href='" + esc(r.link) + "'>" + esc(T("connect")) + "</a></div>";
      }).join("");
    }
    render(false);
    function beep() {
      try { var a = new AudioContext(), o = a.createOscillator(), g = a.createGain(); o.frequency.value = 880; g.gain.value = 0.08; o.connect(g); g.connect(a.destination); o.start(); o.stop(a.currentTime + 0.25); } catch (e) {}
    }
    $("notifbtn").addEventListener("click", function (e) { e.preventDefault(); if (window.Notification) Notification.requestPermission(); });
    crypto.subtle.importKey("jwk", jwk, {name: "ECDH", namedCurve: "P-256"}, false, ["deriveBits"]).then(function (priv) {
      function open(buf) {
        var u = new Uint8Array(buf);
        return crypto.subtle.importKey("raw", u.subarray(0, 65), {name: "ECDH", namedCurve: "P-256"}, false, []).then(function (eph) {
          return crypto.subtle.deriveBits({name: "ECDH", public: eph}, priv, 256);
        }).then(function (shared) {
          var lbl = enc.encode("helpme-channel-v1"), kin = new Uint8Array(lbl.length + 32);
          kin.set(lbl); kin.set(new Uint8Array(shared), lbl.length);
          return crypto.subtle.digest("SHA-256", kin);
        }).then(function (k) {
          return crypto.subtle.importKey("raw", new Uint8Array(k).subarray(0, 16), "AES-GCM", false, ["decrypt"]);
        }).then(function (k) {
          var body = u.subarray(65);
          return crypto.subtle.decrypt({name: "AES-GCM", iv: body.subarray(0, 12)}, k, body.subarray(12));
        }).then(function (pl) { return JSON.parse(dec.decode(pl)); });
      }
      function connectInbox() {
        var sock = new WebSocket(location.origin.replace(/^http/, "ws") + "/helpme/ws?role=inbox&ch=" + encodeURIComponent(id));
        sock.binaryType = "arraybuffer";
        sock.onopen = function () { $("inboxstate").textContent = T("online"); };
        sock.onmessage = function (ev) {
          if (typeof ev.data === "string") return;
          open(ev.data).then(function (r) {
            if (!r.link || r.link.indexOf(location.origin + "/helpme/#") !== 0) return; // only our own viewer links
            if (known.some(function (k) { return k.link === r.link && k.ts === r.ts; })) return;
            known.push(r); save(); render(true); beep();
            if (window.Notification && Notification.permission === "granted") new Notification("HelpMe", {body: L[lang].needs(r.user || "?", r.pc || "?")});
          }, function () {});
        };
        sock.onclose = function () { $("inboxstate").textContent = T("offline"); setTimeout(connectInbox, 3000); };
      }
      connectInbox();
      setInterval(function () { render(false); }, 30000);
    });
  }

  // ------------------------------------------------------------------ joining a session
  function startJoin(t) {
    target = t;
    show("join");
    if (t.mode === "link") {
      var parts = t.frag.split(".");
      try {
        if (parts.length !== 2 || parts[0].length !== 12 || b64.dec(parts[1]).length !== 16) throw 0;
      } catch (e) { $("jstate").textContent = T("badlink"); $("go").disabled = true; return; }
      t.room = parts[0]; t.key = b64.dec(parts[1]);
    }
    try { $("name").value = localStorage.getItem("helpme-name") || ""; } catch (e) {}
    $("name").focus();
  }
  $("go").addEventListener("click", begin);
  $("name").addEventListener("keydown", function (e) { if (e.key === "Enter") begin(); });

  function begin() {
    myName = $("name").value.trim() || (lang === "fr" ? "Assistant" : "Helper");
    try { localStorage.setItem("helpme-name", myName); } catch (e) {}
    $("go").disabled = true;
    $("jstate").textContent = T("connecting");
    connect();
  }

  function seal(u8) {
    var iv = crypto.getRandomValues(new Uint8Array(12));
    return crypto.subtle.encrypt({name: "AES-GCM", iv: iv}, key, u8).then(function (ct) {
      var out = new Uint8Array(12 + ct.byteLength); out.set(iv); out.set(new Uint8Array(ct), 12); return out;
    });
  }
  function unseal(buf) {
    var u = new Uint8Array(buf);
    return crypto.subtle.decrypt({name: "AES-GCM", iv: u.subarray(0, 12)}, key, u.subarray(12)).then(function (p) { return new Uint8Array(p); });
  }
  function send(u8) {
    if (!ws || ws.readyState !== 1 || !key) return;
    var sock = ws;
    seal(u8).then(function (m) { if (sock.readyState === 1) sock.send(m); });
  }
  function sendJSON(type, obj) { var j = enc.encode(JSON.stringify(obj)), m = new Uint8Array(1 + j.length); m[0] = type; m.set(j, 1); send(m); }
  function hello() { sendJSON(MSG.VIEW_HELLO, {name: myName, h264: h264}); $("jstate").textContent = T("waiting"); }

  function connect() {
    key = null; pake = null;
    var q = target.mode === "code" ? "code=" + target.code.slice(0, 4) : "room=" + encodeURIComponent(target.room);
    var sock = ws = new WebSocket(location.origin.replace(/^http/, "ws") + "/helpme/ws?role=view&" + q);
    sock.binaryType = "arraybuffer";
    sock.onmessage = function (ev) {
      if (typeof ev.data === "string") {
        var m = {};
        try { m = JSON.parse(ev.data); } catch (e) {}
        if (m.t === "peer") {
          if (target.mode === "link") {
            crypto.subtle.importKey("raw", target.key, "AES-GCM", false, ["encrypt", "decrypt"]).then(function (k) { key = k; hello(); });
          } else {
            pake = HelpMeSpake2.start(m.room, target.code.slice(4), "B"); // the host's point arrives next
          }
        }
        return;
      }
      var data = ev.data;
      queue = queue.then(function () { return onBinary(data, sock); }).catch(function () {});
    };
    sock.onclose = function (ev) {
      if (ended || sock !== ws) return;
      if (wasLive && Date.now() < reconnectUntil && ev.code !== 4009 && ev.code !== 4029) {
        live = false;
        $("state").textContent = T("restarting");
        setTimeout(connect, 2000);
        return;
      }
      showEnd(ev.code === 4004 ? (wasLive ? T("gone") : T("nosession")) : ev.code === 4009 ? T("busy") : ev.code === 4029 ? T("toomany") : T("gone"));
    };
  }

  function onBinary(data, sock) {
    var u = new Uint8Array(data);
    if (!key) {
      if (u[0] === MSG.PAKE_A && pake && pake.then) {
        return pake.then(function (st) {
          return st.finish(u.subarray(1, 66)).then(function (r) {
            pake = {expected: r.expected, key: r.key};
            var m = new Uint8Array(1 + 65 + 32); m[0] = MSG.PAKE_B; m.set(st.msg, 1); m.set(r.confirm, 66);
            sock.send(m);
          });
        });
      }
      if (u[0] === MSG.PAKE_OK && pake && pake.expected) {
        var got = u.subarray(1), exp = pake.expected, same = got.length === exp.length;
        for (var i = 0; same && i < exp.length; i++) same = got[i] === exp[i];
        if (!same) { showEnd(T("wrongcode")); sock.close(); return; }
        return crypto.subtle.importKey("raw", pake.key, "AES-GCM", false, ["encrypt", "decrypt"]).then(function (k) { key = k; hello(); });
      }
      if (u[0] === MSG.PAKE_FAIL) { showEnd(T("wrongcode")); sock.close(); }
      return;
    }
    return unseal(data).then(handle, function () {});
  }

  function showEnd(msg) {
    ended = true; live = false;
    show("join");
    $("jstate").textContent = msg;
    $("go").style.display = "none";
    if (document.fullscreenElement) document.exitFullscreen().catch(function () {});
  }
  function note(msg) { var n = $("note"); n.textContent = msg; n.style.display = msg ? "block" : "none"; }
  function ack(seq) { var a = new Uint8Array(5); a[0] = MSG.ACK; new DataView(a.buffer).setUint32(1, seq, true); send(a); }

  function handle(m) {
    var t = m[0], dv = new DataView(m.buffer, m.byteOffset, m.byteLength);
    if (t === MSG.STATUS) {
      var st = {};
      try { st = JSON.parse(dec.decode(m.subarray(1))); } catch (e) {}
      if (st.state === "accepted") {
        live = wasLive = true;
        reconnectUntil = Date.now() + 90000;
        show("viewer");
        $("state").textContent = T("live");
        stage.focus();
      } else if (st.state === "denied") {
        showEnd(T("denied")); ws.close();
      } else if (st.state === "stopped") {
        showEnd(T("bye")); ws.close();
      }
    } else if (t === MSG.HELLO) {
      var h = JSON.parse(dec.decode(m.subarray(1)));
      canvas.width = h.w; canvas.height = h.h;
      var sel = $("monsel"), mons = h.monitors || [];
      sel.classList.toggle("hide", mons.length < 2);
      sel.innerHTML = mons.map(function (mo, i) { return "<option value='" + i + "'" + (i === h.monitor ? " selected" : "") + ">" + esc(T("display")) + " " + (i + 1) + " (" + mo.W + "×" + mo.H + ")</option>"; }).join("");
      if (!h.admin) { note(T("notadmin")); setTimeout(function () { if ($("note").textContent === T("notadmin")) note(""); }, 12000); }
      else note("");
    } else if (t === MSG.TILE) {
      var x = dv.getUint16(1, true), y = dv.getUint16(3, true);
      pending.push(createImageBitmap(new Blob([m.subarray(9)], {type: "image/jpeg"})).then(function (bmp) { return {bmp: bmp, x: x, y: y}; }));
    } else if (t === MSG.VIDEO_ON) {
      var v = JSON.parse(dec.decode(m.subarray(1)));
      if (decoder) { try { decoder.close(); } catch (e) {} }
      decoder = new VideoDecoder({output: function (f) { ctx.drawImage(f, 0, 0); f.close(); }, error: function () { decoder = null; }});
      decoder.configure({codec: v.codec, codedWidth: v.w, codedHeight: v.h, optimizeForLatency: true});
      videoTs = 0;
    } else if (t === MSG.VIDEO) {
      if (decoder && decoder.state === "configured") decoder.decode(new EncodedVideoChunk({type: m[1] ? "key" : "delta", timestamp: (videoTs += 50000), data: m.subarray(2)}));
    } else if (t === MSG.VIDEO_OFF) {
      if (decoder) { try { decoder.close(); } catch (e) {} decoder = null; } // a sharp full refresh follows
    } else if (t === MSG.FRAME_END) {
      var seq = dv.getUint32(1, true), tiles = pending;
      pending = [];
      if (decoder && decoder.decodeQueueSize > 2) {
        return new Promise(function (res) {
          var d = decoder;
          d.addEventListener("dequeue", function f() { if (d.decodeQueueSize <= 2) { d.removeEventListener("dequeue", f); res(); } });
          setTimeout(res, 1000);
        }).then(function () { ack(seq); });
      }
      return Promise.all(tiles).then(function (list) {
        list.forEach(function (o) { ctx.drawImage(o.bmp, o.x, o.y); if (o.bmp.close) o.bmp.close(); });
        ack(seq);
      }, function () { ack(seq); });
    } else if (t === MSG.CURSOR) {
      var cx = dv.getUint16(1, true), cy = dv.getUint16(3, true);
      cur.style.display = cx === 65535 ? "none" : "block";
      cur.style.left = (cx / canvas.width * 100) + "%";
      cur.style.top = (cy / canvas.height * 100) + "%";
    } else if (t === MSG.CLIPBOARD) {
      remoteClip = dec.decode(m.subarray(1));
      var btn = $("clipbtn");
      btn.classList.remove("hide"); btn.classList.add("hot");
      if (navigator.clipboard && document.hasFocus()) {
        navigator.clipboard.writeText(remoteClip).then(function () { note(T("clipgot")); setTimeout(function () { note(""); }, 3000); }, function () { note(T("clipready")); });
      } else note(T("clipready"));
    }
  }
  $("clipbtn").addEventListener("click", function () {
    if (navigator.clipboard) navigator.clipboard.writeText(remoteClip).then(function () { note(T("clipgot")); setTimeout(function () { note(""); }, 3000); });
    this.classList.remove("hot");
  });
  $("monsel").addEventListener("change", function () { send(new Uint8Array([MSG.MONITOR, +this.value])); stage.focus(); });

  // ------------------------------------------------------------------ mouse / touch
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

  // ------------------------------------------------------------------ keyboard (set 1 scan codes, by key position)
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
  function keyMsg(sc, ext, down) { var m = new Uint8Array(5); m[0] = MSG.KEY; m[1] = down ? 1 : 0; m[2] = sc & 255; m[3] = sc >> 8; m[4] = ext; return m; }
  function key(e, down) {
    if (!live || e.target === kbd) return;
    // Ctrl+V / Cmd+V: let the browser fire "paste" so the helper's clipboard is sent first.
    if ((e.ctrlKey || e.metaKey) && e.code === "KeyV") return;
    var sc = SC[e.code], ext = 0;
    if (sc === undefined) { sc = SCX[e.code]; ext = 1; }
    if (sc === undefined) return;
    e.preventDefault();
    send(keyMsg(sc, ext, down));
  }
  stage.addEventListener("keydown", function (e) { key(e, true); });
  stage.addEventListener("keyup", function (e) { key(e, false); });
  document.addEventListener("paste", function (e) {
    if (!live || document.activeElement === kbd) return;
    var text = (e.clipboardData || window.clipboardData).getData("text");
    if (!text) return;
    e.preventDefault();
    var t = enc.encode(text), m = new Uint8Array(1 + t.length); m[0] = MSG.CLIPBOARD; m.set(t, 1); send(m);
    var needCtrl = !e.ctrlKey; // with Cmd on a Mac, Ctrl is not held on their side
    if (needCtrl) send(keyMsg(29, 0, true));
    send(keyMsg(47, 0, true)); send(keyMsg(47, 0, false));
    if (needCtrl) send(keyMsg(29, 0, false));
    note(T("pasted")); setTimeout(function () { note(""); }, 2000);
  });

  // Phone keyboards: hidden input, typed text is sent as Unicode characters.
  $("kbbtn").addEventListener("click", function () { kbd.value = ""; kbd.focus(); });
  kbd.addEventListener("input", function () {
    if (!kbd.value) return;
    var t = enc.encode(kbd.value), m = new Uint8Array(1 + t.length);
    m[0] = MSG.TEXT; m.set(t, 1); send(m); kbd.value = "";
  });
  kbd.addEventListener("keydown", function (e) {
    var special = {Backspace: 14, Enter: 28, Tab: 15, Escape: 1};
    if (special[e.key] && live) { e.preventDefault(); send(keyMsg(special[e.key], 0, true)); send(keyMsg(special[e.key], 0, false)); }
  });

  $("fsbtn").addEventListener("click", function () {
    if (document.fullscreenElement) document.exitFullscreen(); else $("viewer").requestFullscreen().catch(function () {});
    stage.focus();
  });
  $("stop").addEventListener("click", function () {
    send(new Uint8Array([MSG.BYE]));
    setTimeout(function () { ended = true; if (ws) ws.close(); showEnd(T("bye")); }, 150);
  });
  window.addEventListener("beforeunload", function () { if (live) send(new Uint8Array([MSG.BYE])); });
})();
