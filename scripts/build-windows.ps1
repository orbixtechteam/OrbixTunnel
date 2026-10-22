# Build the Windows desktop client. Run in PowerShell on Windows.
# Needs: Go, a C compiler on PATH (e.g. mingw-w64 / TDM-GCC) for the Fyne GUI.
$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot\.."

Write-Host ">> building CLI"
Push-Location "$root\cli"; go mod tidy; go build -o "$root\evpn.exe" .; Pop-Location

Write-Host ">> building GUI"
Push-Location "$root\gui"; go mod tidy; go build -ldflags "-H windowsgui" -o "$root\evpn-gui.exe" .; Pop-Location

# bundle sing-box + wintun (Full tunnel engine) next to the app.
# Point SINGBOX_DIR at a folder containing sing-box.exe and wintun.dll,
# or have sing-box.exe on PATH and wintun.dll in the repo root.
$sbDir = $env:SINGBOX_DIR
$sbExe = $null; $wintun = $null
if ($sbDir) {
  $sbExe = Join-Path $sbDir "sing-box.exe"; $wintun = Join-Path $sbDir "wintun.dll"
} else {
  $cmd = Get-Command sing-box.exe -ErrorAction SilentlyContinue
  if ($cmd) { $sbExe = $cmd.Source }
  $wintun = Join-Path $root "wintun.dll"
}
if ($sbExe -and (Test-Path $sbExe)) { Copy-Item $sbExe "$root\sing-box.exe" -Force; Write-Host ">> bundled sing-box.exe" }
else { Write-Host "!! sing-box.exe not found (set SINGBOX_DIR) - Full tunnel will not work" }
if ($wintun -and (Test-Path $wintun)) { if ((Resolve-Path $wintun).Path -ne "$root\wintun.dll") { Copy-Item $wintun "$root\wintun.dll" -Force }; Write-Host ">> bundled wintun.dll" }
else { Write-Host "!! wintun.dll not found (set SINGBOX_DIR) - Full tunnel will not work" }

Write-Host ""
Write-Host "built: $root\evpn.exe (CLI)   $root\evpn-gui.exe (desktop app)"
Write-Host "Full tunnel: run evpn-gui.exe as Administrator. Ship sing-box.exe + wintun.dll in the same folder."
