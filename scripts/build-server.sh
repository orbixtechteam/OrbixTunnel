#!/usr/bin/env bash
# Cross-compile the Linux server binary from any OS (e.g. your Mac), to scp to
# the VPS. Output: evpn-linux-amd64 at the repo root.
set -euo pipefail
cd "$(dirname "$0")/../cli"
go mod tidy
GOOS=linux GOARCH=amd64 go build -o ../orbi-linux-amd64 .
echo "built orbi-linux-amd64"
echo "  scp orbi-linux-amd64 root@YOUR_SERVER:/usr/local/bin/orbi"
echo "  ssh root@YOUR_SERVER 'EVPN_PORT=8443 orbi server -peers /etc/orbi/peers.txt'"
