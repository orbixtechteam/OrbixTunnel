#!/usr/bin/env bash
# Package the macOS desktop client as eVPN.app and eVPN.dmg (double-clickable,
# no terminal). Needs Xcode command line tools.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/gui"

# fyne packaging tool
if ! command -v fyne >/dev/null 2>&1; then
  echo ">> installing fyne tool"
  go install fyne.io/fyne/v2/cmd/fyne@latest
fi
export PATH="$PATH:$(go env GOPATH)/bin"

# icon
if [ ! -f Icon.png ]; then
  echo ">> generating icon"
  go run "$root/scripts/mkicon.go" Icon.png
fi

echo ">> packaging eVPN.app"
go mod tidy
rm -rf "$root/eVPN.app"
fyne package -os darwin -name eVPN -icon Icon.png -app-id com.evpn.client
mv -f eVPN.app "$root/eVPN.app"

echo ">> building eVPN.dmg"
cd "$root"
rm -f eVPN.dmg
hdiutil create -volname eVPN -srcfolder eVPN.app -ov -format UDZO eVPN.dmg

echo
echo "built:  $root/eVPN.app   $root/eVPN.dmg"
echo "Open the .dmg and drag eVPN to Applications. Double-click to run."
echo "Full tunnel asks for your admin password (needs: brew install sing-box)."
