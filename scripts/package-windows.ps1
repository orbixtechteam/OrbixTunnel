# Package the Windows desktop client into ONE zip:
#   Orbix Tunnel.exe (icon, version info, asks for admin via UAC)
#   + sing-box.exe + wintun.dll (Full tunnel engine) + README.txt + LICENSE
#
# Run in PowerShell on Windows:   .\scripts\package-windows.ps1 [-Version 0.1.0]
# Needs:    Go 1.22+ and a C compiler on PATH (mingw-w64) - Fyne needs it.
#           Internet access (downloads sing-box and wintun), unless you set
#           $env:SINGBOX_DIR to a folder that already has sing-box.exe + wintun.dll
# Output:   dist\Orbix-Tunnel-<Version>-windows-amd64.zip
param([string]$Version = "0.1.0")

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$root = (Resolve-Path "$PSScriptRoot\..").Path
$App  = "Orbix Tunnel"
$dist = Join-Path $root "dist"
$pkg  = Join-Path $dist $App
$tmp  = Join-Path $dist "tmp"

function Check([string]$what) {
  if ($LASTEXITCODE -ne 0) { throw "$what failed (exit code $LASTEXITCODE)" }
}

foreach ($tool in @("go", "gcc")) {
  if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
    throw "$tool not found on PATH. Install Go (winget install GoLang.Go) and mingw-w64 (winget install BrechtSanders.WinLibs.POSIX.UCRT), then open a NEW PowerShell window."
  }
}

if (Test-Path $dist) { Remove-Item $dist -Recurse -Force }
New-Item -ItemType Directory -Force $pkg | Out-Null
New-Item -ItemType Directory -Force $tmp | Out-Null

# --- 1. icon + Windows resources (icon, version info, UAC admin manifest) ----
Push-Location "$root\gui"
try {
  Write-Host ">> generating icon"
  go run "$root\scripts\mkicon.go" Icon.png
  Check "icon generation"

  Write-Host ">> embedding icon, version info and admin manifest"
  go install github.com/tc-hib/go-winres@latest
  Check "go-winres install"
  $env:PATH = "$env:PATH;$(go env GOPATH)\bin"
  go-winres simply --arch amd64 --icon Icon.png --manifest gui --admin `
    --product-name $App --file-description $App --original-filename "$App.exe" `
    --copyright "(c) 2026 Orbix Lab" --file-version "$Version.0" --product-version $Version
  Check "go-winres"

  # --- 2. build the GUI ------------------------------------------------------
  Write-Host ">> building $App.exe (first build takes a few minutes)"
  $env:CGO_ENABLED = "1"
  $env:GOOS = "windows"
  $env:GOARCH = "amd64"
  go mod tidy
  Check "go mod tidy"
  go build -ldflags "-H windowsgui -s -w" -o "$pkg\$App.exe" .
  Check "go build"
} finally {
  Pop-Location
}

# --- 3. sing-box + wintun -----------------------------------------------------
if ($env:SINGBOX_DIR) {
  Write-Host ">> using sing-box + wintun from $($env:SINGBOX_DIR)"
  Copy-Item (Join-Path $env:SINGBOX_DIR "sing-box.exe") $pkg
  Copy-Item (Join-Path $env:SINGBOX_DIR "wintun.dll") $pkg
} else {
  Write-Host ">> downloading sing-box (latest release)"
  $rel = Invoke-RestMethod "https://api.github.com/repos/SagerNet/sing-box/releases/latest" -Headers @{ "User-Agent" = "orbix-tunnel-package" }
  $asset = $rel.assets | Where-Object { $_.name -match 'windows-amd64\.zip$' -and $_.name -notmatch 'legacy' } | Select-Object -First 1
  if (-not $asset) { throw "no windows-amd64 sing-box asset found in the latest release" }
  Invoke-WebRequest $asset.browser_download_url -OutFile "$tmp\singbox.zip"
  Expand-Archive "$tmp\singbox.zip" "$tmp\singbox" -Force
  $sbExe = Get-ChildItem "$tmp\singbox" -Recurse -Filter "sing-box.exe" | Select-Object -First 1
  if (-not $sbExe) { throw "sing-box.exe not found inside $($asset.name)" }
  Copy-Item $sbExe.FullName $pkg
  $sbLic = Get-ChildItem "$tmp\singbox" -Recurse -Filter "LICENSE*" | Select-Object -First 1
  if ($sbLic) { Copy-Item $sbLic.FullName "$pkg\sing-box-LICENSE.txt" }

  Write-Host ">> downloading wintun"
  Invoke-WebRequest "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile "$tmp\wintun.zip"
  Expand-Archive "$tmp\wintun.zip" "$tmp\wintun" -Force
  Copy-Item "$tmp\wintun\wintun\bin\amd64\wintun.dll" $pkg
}
Write-Host ("   " + (& "$pkg\sing-box.exe" version | Select-Object -First 1) + "  (Full tunnel needs 1.12 or newer)")

# --- 4. docs + zip -------------------------------------------------------------
Copy-Item "$root\LICENSE" "$pkg\LICENSE.txt"
@"
Orbix Tunnel

1. Extract this whole folder somewhere (do not run it from inside the zip).
2. Double-click "Orbix Tunnel.exe" and accept the Windows admin prompt
   (needed for Full tunnel).
3. Import your .orb profile, select it, tick Full tunnel if you want the whole
   device tunnelled, then Connect.

Keep sing-box.exe and wintun.dll in the same folder as the app.

The app is not code-signed, so Windows SmartScreen may warn on first launch:
click "More info" > "Run anyway".
"@ | Set-Content -Encoding UTF8 "$pkg\README.txt"

$zip = Join-Path $dist "Orbix-Tunnel-$Version-windows-amd64.zip"
Compress-Archive -Path $pkg -DestinationPath $zip -Force
Remove-Item $tmp -Recurse -Force

Write-Host ""
Write-Host "built:  $zip"
Write-Host "folder: $pkg"
