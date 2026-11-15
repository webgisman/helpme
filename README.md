# Guxos HelpMe

**Free, open-source remote help — end-to-end encrypted, nothing to install for the helper.**
Aide à distance gratuite et open source — chiffrée de bout en bout, rien à installer pour la personne qui aide.

https://guxos.com/helpme/

1. The person who needs help runs `HelpMe.exe` (Windows 10/11, 7 MB, no install). It shows a link and copies it.
2. The helper opens the link in any browser (PC, Mac, Linux, phone).
3. The person helped must click **Yes** before anything is shared. Closing the HelpMe window ends everything.

## Security model
- **End-to-end encryption**: the link is `https://guxos.com/helpme/#<room>.<key>`. The 128-bit key is in the URL fragment, which browsers never send to servers. Every message is AES-128-GCM encrypted with a random nonce (`internal/proto`); the relay (`cmd/relay`) only forwards ciphertext and never sees the key.
- **Explicit consent** for every connection (Windows dialog, default button = No), visible "session active" state, one helper at a time.
- **No accounts, no passwords, no unattended access**: a session only exists while `HelpMe.exe` runs.
- The relay accepts helpers only from the guxos.com origin and refuses browser pages posing as hosts; 8 h max per session.

## Layout
| Path | What |
|---|---|
| `cmd/helpme` | Windows host: GDI capture, changed-tile JPEG compression, SendInput, consent dialog (no cgo, no dependency) |
| `cmd/relay` | WebSocket relay (Docker, `deploy/`) |
| `web/` | landing page + browser viewer (WebCrypto, canvas) |
| `internal/proto` | message format and encryption |
| `cmd/viewcheck` | command-line test viewer |

## Build
```
sh deploy/build.sh      # tests + build/HelpMe.exe + build/helpme-relay
```

## Roadmap
- H.264 video mode (Windows Media Foundation hardware encoder) for fast-moving content
- macOS and Linux hosts, multi-monitor, file transfer, code signing

License: AGPL-3.0
