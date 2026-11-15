# Guxos HelpMe

**Free, open-source remote help — end-to-end encrypted, nothing to install for the helper.**
Aide à distance gratuite et open source — chiffrée de bout en bout, rien à installer pour la personne qui aide.

https://guxos.com/helpme/

1. The person who needs help runs `HelpMe.exe` (Windows 10/11, 8 MB, no install). It shows a **9-digit code** and a link.
2. The helper types the code at https://guxos.com/helpme/ (or opens the link) in any browser: PC, Mac, Linux, phone.
3. The person helped must click **Yes** before anything is shared, and can press **Stop** at any time.

Also: clipboard sync both ways (Ctrl+V in the browser pastes on the remote PC), multiple displays,
"Run as administrator" to control elevated windows, and **help channels**: a helper (family member,
IT department) creates a channel once; the people they help get a HelpMe with a **Request help**
button that drops the code into the helper's inbox page.

## Security model
- **End-to-end encryption**: with a link (`https://guxos.com/helpme/#<room>.<key>`) the 128-bit key is in the URL fragment, which browsers never send to servers. With a short code, both sides run **SPAKE2** (RFC 9382, P-256, `internal/spake2`, `web/spake2.js`): the relay cannot test codes offline, every guess is an online attempt, and the host changes its code after 3 failures. Messages are AES-128-GCM (`internal/proto`); the relay (`cmd/relay`) only forwards ciphertext.
- **Help channels** use public-key encryption (P-256 ECDH + AES-GCM): the download link or `helpme.json` only carries the public key, so it can send requests but never read them.
- **Explicit consent** for every connection (Windows dialog, default button = No), visible "session active" state, one helper at a time.
- **No accounts, no passwords, no unattended access**: a session only exists while `HelpMe.exe` runs.
- The relay accepts helpers only from the guxos.com origin and refuses browser pages posing as hosts; 8 h max per session.

## Layout
| Path | What |
|---|---|
| `cmd/helpme` | Windows host: native window, GDI capture, changed-tile JPEG, H.264 via Media Foundation, SendInput, clipboard (no cgo) |
| `cmd/relay` | WebSocket relay (Docker, `deploy/`) |
| `web/` | landing page + browser viewer (WebCrypto, canvas) |
| `internal/proto` | message format, session and channel encryption |
| `internal/spake2` | SPAKE2 for short codes |
| `cmd/viewcheck` | command-line test viewer |

## Build
```
sh deploy/build.sh      # tests + build/HelpMe.exe + build/helpme-relay
```

## Roadmap
- macOS and Linux hosts, file transfer, push notifications for help channels, code signing

## Deploying in a company
Create a help channel at https://guxos.com/helpme/, then deploy `HelpMe.exe` with the `helpme.json` the
inbox page gives you (or `HelpMe.exe -channel <id>.<key> -helper-name "IT support"`). Allow
`wss://guxos.com` on port 443 (exclude it from SSL inspection) and whitelist the SHA-256 in your EDR.
The relay can also be self-hosted (`deploy/`).

License: AGPL-3.0
