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

# bundle sing-box (Full tunnel engine) so users don't have to install it.
# Override the source with SINGBOX_BIN=/path/to/sing-box.
SB="${SINGBOX_BIN:-$(command -v sing-box || true)}"
if [ -n "$SB" ] && [ -f "$SB" ]; then
  echo ">> bundling sing-box from $SB"
  cp -L "$SB" "$root/$APP.app/Contents/MacOS/sing-box"
  chmod 755 "$root/$APP.app/Contents/MacOS/sing-box"
else
  echo "!! sing-box not found (brew install sing-box, or set SINGBOX_BIN) - Full tunnel will not work in this build"
fi

# Ad-hoc sign (no Apple account needed), like electron-builder does. Without a
# valid signature, Macs that get the app via download/AirDrop show "is damaged
# and can't be opened". Strip stray attributes and the Go linker's own
# signature first, sign the bundled sing-box, then the whole app, then verify.
echo ">> ad-hoc signing"
APPDIR="$root/$APP.app"
xattr -cr "$APPDIR"
EXE="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$APPDIR/Contents/Info.plist")"
codesign --remove-signature "$APPDIR/Contents/MacOS/$EXE" 2>/dev/null || true
if [ -f "$APPDIR/Contents/MacOS/sing-box" ]; then
  codesign --force --sign - "$APPDIR/Contents/MacOS/sing-box"
fi
codesign --force --deep --sign - "$APPDIR"
codesign --verify --deep --strict --verbose=2 "$APPDIR"

echo ">> building $APP.dmg"
cd "$root"
rm -f "$APP.dmg"
# size the image explicitly (hdiutil's auto-estimate runs out of space with the
# bundled sing-box binary): app size + 100 MB headroom.
SIZE_MB=$(( $(du -sm "$APP.app" | cut -f1) + 100 ))
hdiutil create -volname "$APP" -srcfolder "$APP.app" -ov -fs HFS+ -size "${SIZE_MB}m" -format UDZO "$APP.dmg"

echo
echo "built:  $root/$APP.app   $root/$APP.dmg"
echo "Open the .dmg and drag $APP to Applications. Double-click to run."
echo "On another Mac (not notarized): right-click the app > Open, or run: xattr -cr \"/Applications/$APP.app\""
echo "Full tunnel asks for your admin password (sing-box is bundled in the app)."
