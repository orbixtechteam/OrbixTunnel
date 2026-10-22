#!/usr/bin/env bash
# Package the macOS desktop client as eVPN.app and eVPN.dmg (double-clickable,
# no terminal). Needs Xcode command line tools.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/gui"

# fyne packaging tool
if ! command -v fyne >/dev/null 2>&1; then
  echo ">> installing fyne tool"
  go install fyne.io/tools/cmd/fyne@latest
fi
export PATH="$PATH:$(go env GOPATH)/bin"

# icon (always regenerate so logo/theme changes apply)
echo ">> generating icon"
go run "$root/scripts/mkicon.go" Icon.png

echo ">> packaging Orbi-VPN.app"
go mod tidy
rm -rf "$root/Orbi-VPN.app"
fyne package -os darwin -name Orbi-VPN -icon Icon.png -id com.orbi.vpn
mv -f Orbi-VPN.app "$root/Orbi-VPN.app"

echo ">> building Orbi-VPN.dmg"
cd "$root"
rm -f Orbi-VPN.dmg
hdiutil create -volname Orbi-VPN -srcfolder Orbi-VPN.app -ov -format UDZO Orbi-VPN.dmg

echo
echo "built:  $root/Orbi-VPN.app   $root/Orbi-VPN.dmg"
echo "Open the .dmg and drag Orbi-VPN to Applications. Double-click to run."
echo "Full tunnel asks for your admin password (needs: brew install sing-box)."
