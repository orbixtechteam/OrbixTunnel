#!/usr/bin/env bash
# Build the macOS desktop client (and the CLI). Needs Xcode command line tools
# (xcode-select --install) for the Fyne GUI.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"

echo ">> building CLI"
( cd "$root/cli" && go mod tidy && go build -o "$root/evpn" . )

echo ">> building GUI"
( cd "$root/gui" && go mod tidy && go build -o "$root/evpn-gui" . )

echo
echo "built: $root/evpn (CLI)   $root/evpn-gui (desktop app)"
echo "run GUI: $root/evpn-gui    (do NOT use sudo; Full tunnel asks for admin itself)"
echo "Full tunnel needs: brew install sing-box"
