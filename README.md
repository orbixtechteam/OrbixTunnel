# eVPN

A small anti-censorship tunnel. The carrier looks like a telemetry API
(`POST /api/v2/telemetry/ingest`); a WebSocket upgrade on that path opens the
real tunnel: one yamux session, each connection a multiplexed stream, TCP and
UDP. Auth is WireGuard-like — a server peers file plus per-client profiles.

## Layout

```
cli/      server + client + profile generator (one Go binary: evpn)
gui/      macOS/Windows desktop client (Fyne) with multiple profiles
scripts/  build & install helpers
```

## Server (Debian/Ubuntu VPS)

One command — installs Go if needed, builds, and runs as a systemd service on
tcp/8443:

```bash
git clone <your-repo> evpn && cd evpn
bash scripts/install-server.sh 8443
```

Then create a client profile (adds a token to the peers file; no restart
needed — new tokens are accepted live):

```bash
orbi gen -name telefonum -server $(curl -s ifconfig.me) -port 8443 \
  -peers /etc/orbi/peers.txt -out telefonum.orb
```

Copy `telefonum.orb` to the client. Manage the service with
`systemctl status|restart|stop orbi`. Revoke a client by deleting its line in
`/etc/orbi/peers.txt`.

Prefer not to install on the server? Build the Linux binary locally and copy it:

```bash
bash scripts/build-server.sh                 # -> orbi-linux-amd64
scp orbi-linux-amd64 root@SERVER:/usr/local/bin/orbi
```

## Client — desktop app

### macOS
```bash
xcode-select --install        # once, for the Fyne GUI toolchain
bash scripts/build-mac.sh     # quick: -> ./evpn-gui (and ./evpn CLI)
bash scripts/package-mac.sh   # app: -> eVPN.app + eVPN.dmg (double-clickable)
```
Open `eVPN.dmg`, drag **eVPN** to Applications, launch it. SOCKS mode needs no
admin; **Full tunnel** asks for your admin password (needs `brew install
sing-box`). From a terminal you can also run `./evpn-gui` or `sudo ./evpn-gui`.

### Windows
Install Go and a C compiler (mingw-w64 / TDM-GCC on PATH), then in PowerShell:
```powershell
scripts\build-windows.ps1     # -> evpn-gui.exe (and evpn.exe CLI)
```
Run `evpn-gui.exe`. For **Full tunnel**, run it as Administrator and put
`sing-box.exe` + `wintun.dll` next to it.

### Using the app
1. `Import...` the `.orb` profile (stored under your user config dir).
2. Select it, optionally tick **Full tunnel (TUN)**, press **Connect**.
3. The exit IP is shown once connected.

- **SOCKS mode** (no admin): local SOCKS5 on `127.0.0.1:1080` — point your
  browser/apps at it. Routes what honours a proxy.
- **Full tunnel**: the app drives sing-box to capture the whole device (fakeip
  DNS so names resolve server-side, UDP/QUIC allowed, the server IP kept direct
  to avoid a loop). Needs admin/root.

Multiple profiles are kept in a list; one tunnel is active at a time.

## Client — CLI (optional)

```bash
cd cli && go build -o ../evpn .
./evpn client -c telefonum.orb         # local SOCKS5 on 127.0.0.1:1080
curl --socks5-hostname 127.0.0.1:1080 https://ifconfig.me
```

## Profile format

```
name   = telefonum
server = 138.197.214.117
port   = 8443
path   = /api/v2/telemetry/ingest
tls    = false
token  = <64-hex, secret>
socks  = 127.0.0.1:1080
```

`.orb` profiles and `peers.txt` carry secrets — they are gitignored; keep them
private (`chmod 600`).

## Notes

- Only IPv4 is tunnelled; IPv6-only endpoints (e.g. `*-v6.*` probe hosts) won't
  connect — expected for an IPv4 tunnel.
- `tls = true` wraps the carrier in `wss://` (needs a TLS front on the server).
