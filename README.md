# Orbi-VPN

A lightweight, self-hosted anti-censorship tunnel. The carrier looks like an
ordinary telemetry API (`POST /api/v2/telemetry/ingest`); a WebSocket upgrade on
that same path opens the real tunnel. Traffic is multiplexed over one connection
with [yamux](https://github.com/hashicorp/yamux), carries both **TCP and UDP**
(so QUIC, DNS and games work), and authenticates WireGuard-style with a
server-side peers file and per-client profiles.

- **Server** — a single Go binary (`orbi`) you run on any VPS.
- **Client** — a cross-platform desktop app (macOS / Windows) with a profile
  list, plus a CLI for scripting.
- **Full tunnel** — routes the whole device through [sing-box](https://sing-box.sagernet.org)
  (fake-IP DNS so names resolve server-side, UDP allowed, the server IP kept
  direct). Needs admin/root.
- **Proxy mode** — a local SOCKS5 on `127.0.0.1:1080`, no admin needed.

> Built for circumventing network censorship and running your own private VPN.
> Use it only on networks and traffic you are authorized to tunnel.

---

## How it works

```
app ─▶ SOCKS5 (local)          ┌─ WebSocket /api/v2/telemetry/ingest ─┐
      or TUN (sing-box) ─▶ orbi client ──── yamux (TCP+UDP streams) ──── orbi server ─▶ internet
```

1. On the client, each connection becomes one yamux stream over a single
   WebSocket to the server.
2. The server dials the real destination (TCP) or relays datagrams (UDP) and
   pipes the bytes back.
3. A plain HTTP request to the path (or a wrong token) just returns
   `{"ok":true,"app":"orbix-telemetry"}` — it looks like a boring API.

---

## Repository layout

```
cli/      server + client + profile generator (one binary: orbi)
gui/      desktop client (Fyne), multiple profiles, forced dark UI
scripts/  build & install helpers
```

---

## Requirements

- **Server:** a Linux VPS (Debian/Ubuntu). The installer pulls Go automatically.
- **Build from source:** Go 1.22+.
- **Desktop GUI:** a C toolchain (macOS: `xcode-select --install`; Windows:
  mingw-w64 / TDM-GCC on `PATH`) — Fyne needs it.
- **Full tunnel:** [sing-box](https://sing-box.sagernet.org) on the client
  (`brew install sing-box`; on Windows also `wintun.dll`).

---

## Server

### Install (one command)

```bash
git clone https://github.com/<you>/orbi-vpn.git && cd orbi-vpn
sudo bash scripts/install-server.sh 8443
```

This installs Go if needed, builds `/usr/local/bin/orbi`, creates
`/etc/orbi/peers.txt`, opens the firewall port, and starts the `orbi` systemd
service.

### Create a client profile

```bash
orbi gen -name phone -server $(curl -s ifconfig.me) -port 8443 \
  -peers /etc/orbi/peers.txt -out phone.orb
```

A random token is generated, appended to the peers file (**no restart needed** —
new tokens are accepted live), and written into `phone.orb`. Copy that file to
the client and import it.

### Manage the service

```bash
systemctl status orbi        # state
systemctl restart orbi       # restart
systemctl stop orbi          # stop
systemctl start orbi         # start
journalctl -u orbi -f        # live logs
```

### Update to the latest version

```bash
cd orbi-vpn
git pull
cd cli && go build -o /usr/local/bin/orbi . && cd ..
systemctl restart orbi
```

(The peers file and existing profiles are untouched by a rebuild.)

### Revoke a client

Delete its line in `/etc/orbi/peers.txt`. The next connection with that token is
refused — no restart needed.

```bash
nano /etc/orbi/peers.txt     # remove the "<token> name" line
```

### Uninstall

```bash
systemctl disable --now orbi
rm -f /etc/systemd/system/orbi.service /usr/local/bin/orbi
rm -rf /etc/orbi
systemctl daemon-reload
```

### Build the server binary elsewhere (no Go on the VPS)

```bash
bash scripts/build-server.sh                 # -> orbi-linux-amd64
scp orbi-linux-amd64 root@SERVER:/usr/local/bin/orbi
ssh root@SERVER 'systemctl restart orbi'
```

---

## Client — desktop app

### macOS

```bash
xcode-select --install          # once
bash scripts/build-mac.sh       # quick build -> ./evpn-gui
bash scripts/package-mac.sh     # app bundle -> Orbi-VPN.app + Orbi-VPN.dmg
```

Open `Orbi-VPN.dmg`, drag **Orbi-VPN** to Applications, launch it.

### Windows

Install Go and a C compiler (mingw-w64 / TDM-GCC on `PATH`), then in PowerShell:

```powershell
scripts\build-windows.ps1       # -> evpn-gui.exe
```

For **Full tunnel**, run `evpn-gui.exe` as Administrator and put `sing-box.exe` +
`wintun.dll` next to it.

### Using the app

1. **Import...** your `.orb` profile (stored in your user config dir; multiple
   profiles are kept in a list).
2. Select a profile. Optionally tick **Full tunnel**.
3. **Connect**. The exit IP shows once connected.

- **Proxy mode** (no admin): SOCKS5 on `127.0.0.1:1080` — point your browser/apps
  at it.
- **Full tunnel** (admin): captures the whole device via sing-box. On macOS the
  app asks for your admin password automatically; on Windows run it elevated.

One tunnel is active at a time.

---

## Client — CLI

```bash
cd cli && go build -o orbi .
./orbi client -c phone.orb                     # SOCKS5 on 127.0.0.1:1080
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
```

The `orbi` binary does everything: `orbi server`, `orbi client -c FILE`,
`orbi gen ...`. Run `orbi` with no args for usage.

---

## Profile format (`.orb`)

```
name   = phone
server = 203.0.113.10
port   = 8443
path   = /api/v2/telemetry/ingest
tls    = false
token  = <64-hex, secret>
socks  = 127.0.0.1:1080
```

`.orb` files and `peers.txt` carry secrets — they are gitignored. Keep them
private (`chmod 600`).

### Optional TLS

Set `tls = true` in the profile (and front the server with TLS) to wrap the
carrier in `wss://` so it looks like normal HTTPS.

---

## Environment variables

Both sides also read env vars (useful for systemd / scripting):

| var                | meaning                        | default                       |
|--------------------|--------------------------------|-------------------------------|
| `EVPN_PORT`        | server listen port             | `8443`                        |
| `EVPN_PATH`        | carrier path                   | `/api/v2/telemetry/ingest`    |
| `EVPN_PEERS`       | peers file                     | `/etc/orbi/peers.txt` (gen)   |
| `EVPN_SERVER_HOST` | client: server host            | `127.0.0.1`                   |
| `EVPN_SERVER_PORT` | client: server port            | `8443`                        |
| `EVPN_TOKEN`       | single-token mode (no peers)   | —                             |
| `EVPN_SOCKS`       | client: local SOCKS listen     | `127.0.0.1:1080`              |
| `EVPN_TLS`         | `1` to use `wss://`            | —                             |

---

## Troubleshooting

- **`bind: address already in use`** — another `orbi`/`evpn` process holds the
  port. `ss -ltnp | grep 8443`, then `pkill -f 'orbi server'` and restart the
  service. Don't mix `nohup` and systemd.
- **Service keeps restarting / `status=1`** — run it in the foreground to see the
  real error: `/usr/local/bin/orbi server -peers /etc/orbi/peers.txt`.
- **An IPv6-only site won't open** (e.g. `*-v6.*` probe hosts) — expected; only
  IPv4 is tunnelled.
- **GUI build fails with a C/OpenGL error** — install the C toolchain (see
  Requirements). Fyne can't build without it.
- **Full tunnel says sing-box not found** — install it, or place the binary next
  to the app.

---

## Building / contributing

```bash
# server + CLI
cd cli && go build -o orbi .
# desktop app
cd gui && go mod tidy && go build -o orbi-gui .
```

Issues and pull requests welcome.

## License

Add your license here (e.g. MIT).
