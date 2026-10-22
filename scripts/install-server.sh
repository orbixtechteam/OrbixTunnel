#!/usr/bin/env bash
# Full server install for a Debian/Ubuntu VPS. Installs Go if needed, builds the
# carrier, creates a peers file, and runs it as the "orbi" systemd service.
#
# Usage (as root on the VPS):   bash scripts/install-server.sh [PORT]
set -euo pipefail

PORT="${1:-8443}"
GOVER="1.23.4"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

echo ">> Orbix Tunnel server install (port ${PORT})"

# --- Go -----------------------------------------------------------------
need_go=1
if command -v go >/dev/null 2>&1; then
  have="$(go version | awk '{print $3}' | sed 's/go//')"
  major="$(echo "$have" | cut -d. -f1)"; minor="$(echo "$have" | cut -d. -f2)"
  if [ "$major" -gt 1 ] || { [ "$major" -eq 1 ] && [ "$minor" -ge 22 ]; }; then need_go=0; fi
fi
if [ "$need_go" -eq 1 ]; then
  echo ">> installing Go ${GOVER}"
  arch="$(dpkg --print-architecture 2>/dev/null || echo amd64)"
  curl -fsSL "https://go.dev/dl/go${GOVER}.linux-${arch}.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz
  export PATH="/usr/local/go/bin:$PATH"
  grep -q '/usr/local/go/bin' /etc/profile || echo 'export PATH=/usr/local/go/bin:$PATH' >> /etc/profile
fi
export PATH="/usr/local/go/bin:$PATH"

# --- build --------------------------------------------------------------
echo ">> building carrier"
( cd "${REPO_DIR}/cli" && go mod tidy && go build -o /usr/local/bin/orbi . )

# --- peers + service ----------------------------------------------------
mkdir -p /etc/orbi
touch /etc/orbi/peers.txt
chmod 600 /etc/orbi/peers.txt

cat > /etc/systemd/system/orbi.service <<EOF
[Unit]
Description=Orbix Tunnel carrier
After=network.target

[Service]
Environment=EVPN_PORT=${PORT}
ExecStart=/usr/local/bin/orbi server -peers /etc/orbi/peers.txt
Restart=always
RestartSec=2
User=root

[Install]
WantedBy=multi-user.target
EOF

command -v ufw >/dev/null 2>&1 && ufw allow "${PORT}/tcp" || true

systemctl daemon-reload
systemctl enable --now orbi.service

echo ">> done. status:"
systemctl --no-pager status orbi.service | head -n 6 || true
echo
echo "Create a client profile with:"
echo "  orbi gen -name NAME -server \$(curl -s ifconfig.me) -port ${PORT} -peers /etc/orbi/peers.txt -out NAME.orb"
