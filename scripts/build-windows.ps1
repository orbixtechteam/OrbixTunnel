# Build the Windows desktop client. Run in PowerShell on Windows.
# Needs: Go, a C compiler on PATH (e.g. mingw-w64 / TDM-GCC) for the Fyne GUI.
$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot\.."

Write-Host ">> building CLI"
Push-Location "$root\cli"; go mod tidy; go build -o "$root\evpn.exe" .; Pop-Location

Write-Host ">> building GUI"
Push-Location "$root\gui"; go mod tidy; go build -ldflags "-H windowsgui" -o "$root\evpn-gui.exe" .; Pop-Location

Write-Host ""
Write-Host "built: $root\evpn.exe (CLI)   $root\evpn-gui.exe (desktop app)"
Write-Host "Full tunnel: run evpn-gui.exe as Administrator; place sing-box.exe + wintun.dll next to it."
