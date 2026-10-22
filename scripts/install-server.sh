#!/usr/bin/env bash
# Full server install for a Debian/Ubuntu VPS. Installs Go if needed, builds the
# carrier, creates a peers file, and runs it as a systemd service on tcp/8443.
#
# Usage (as root on the VPS):   bash scripts/install-server.sh [PORT]
set -euo pipefail

PORT="${1:-8443}"
GOVER="1.23.4"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

echo ">> eVPN server install (port ${PORT})"

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
( cd "${REPO_DIR}/cli" && go mod tidy && go build -o /usr/local/bin/evpn . )

# --- peers + service ----------------------------------------------------
mkdir -p /etc/evpn
touch /etc/evpn/peers.txt
chmod 600 /etc/evpn/peers.txt

cat > /etc/systemd/system/evpn.service <<EOF
[Unit]
Description=eVPN carrier
After=network.target

[Service]
Environment=EVPN_PORT=${PORT}
ExecStart=/usr/local/bin/evpn server -peers /etc/evpn/peers.txt
Restart=always
RestartSec=2
User=root

[Install]
WantedBy=multi-user.target
EOF

command -v ufw >/dev/null 2>&1 && ufw allow "${PORT}/tcp" || true

systemctl daemon-reload
systemctl enable --now evpn.service

echo ">> done. status:"
systemctl --no-pager status evpn.service | head -n 6 || true
echo
echo "Create a client profile with:"
echo "  evpn gen -name NAME -server \$(curl -s ifconfig.me) -port ${PORT} -peers /etc/evpn/peers.txt -out NAME.evpn"
