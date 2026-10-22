#!/usr/bin/env bash
# Package the macOS desktop client as "Orbix Tunnel.app" and "Orbix Tunnel.dmg"
# (double-clickable, no terminal). Needs Xcode command line tools.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/gui"
APP="Orbix Tunnel"

# fyne packaging tool
if ! command -v fyne >/dev/null 2>&1; then
  echo ">> installing fyne tool"
  go install fyne.io/tools/cmd/fyne@latest
fi
export PATH="$PATH:$(go env GOPATH)/bin"

# icon (always regenerate so logo/theme changes apply)
echo ">> generating icon"
go run "$root/scripts/mkicon.go" Icon.png

echo ">> packaging $APP.app"
go mod tidy
rm -rf "$root/$APP.app"
fyne package -os darwin -name "$APP" -icon Icon.png -id com.orbixlab.orbi
mv -f "$APP.app" "$root/$APP.app"

echo ">> building $APP.dmg"
cd "$root"
rm -f "$APP.dmg"
hdiutil create -volname "$APP" -srcfolder "$APP.app" -ov -format UDZO "$APP.dmg"

echo
echo "built:  $root/$APP.app   $root/$APP.dmg"
echo "Open the .dmg and drag $APP to Applications. Double-click to run."
echo "Full tunnel asks for your admin password (needs: brew install sing-box)."
