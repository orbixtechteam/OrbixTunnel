# Orbix Tunnel - Windows packager. Builds the app and bundles everything into
# ONE zip:  "Orbix Tunnel.exe" (icon, version info, asks for admin via UAC)
#           + sing-box.exe + wintun.dll + README.txt
#
# Run in PowerShell, from the repo root (the folder that contains gui\):
#     Set-ExecutionPolicy -Scope Process Bypass
#     .\scripts\package-windows.ps1            # or:  .\package-windows.ps1  (copy anywhere inside the repo root)
#
# Needs: Go 1.22+ and a C compiler on PATH (mingw-w64) for the Fyne GUI, plus
#        internet access. sing-box and wintun are downloaded automatically.
# Optional: $env:SINGBOX_DIR = a folder that already has sing-box.exe and/or
#        wintun.dll - files found there are used, anything missing is downloaded.
# Output: dist\Orbix-Tunnel-<Version>-windows-amd64.zip  (and dist\Orbix Tunnel\)
param([string]$Version = "0.1.0")

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# --- locate the repo root -----------------------------------------------------
if (Test-Path (Join-Path (Get-Location).Path "gui\main.go")) {
  $root = (Get-Location).Path
} elseif ($PSScriptRoot -and (Test-Path (Join-Path $PSScriptRoot "..\gui\main.go"))) {
  $root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
} elseif ($PSScriptRoot -and (Test-Path (Join-Path $PSScriptRoot "gui\main.go"))) {
  $root = $PSScriptRoot
} else {
  throw "Run this from the repo root (the folder that contains gui\)."
}

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

# --- 1. build the app (with icon, version info and admin manifest) -------------
Push-Location "$root\gui"
try {
  # icon: use gui\Icon.png if present, else generate it when the generator exists
  $mk = Join-Path $root "scripts\mkicon.go"
  if ((-not (Test-Path "Icon.png")) -and (Test-Path $mk)) {
    Write-Host ">> generating icon"
    go run $mk Icon.png
  }

  Write-Host ">> embedding icon, version info and admin manifest"
  $winresOk = $true
  go install github.com/tc-hib/go-winres@latest
  if ($LASTEXITCODE -ne 0) { $winresOk = $false }
  if ($winresOk) {
    $env:PATH = "$env:PATH;$(go env GOPATH)\bin"
    $winres = @("simply", "--arch", "amd64", "--manifest", "gui", "--admin",
                "--product-name", $App, "--file-description", $App,
                "--original-filename", "$App.exe", "--copyright", "(c) 2026 Orbix Lab",
                "--file-version", "$Version.0", "--product-version", $Version)
    if (Test-Path "Icon.png") { $winres += @("--icon", "Icon.png") }
    go-winres @winres
    if ($LASTEXITCODE -ne 0) { $winresOk = $false }
  }
  if (-not $winresOk) {
    Write-Host "!! could not embed icon/admin manifest - building without them (run the app as Administrator for Full tunnel)"
  }

  Write-Host ">> building $App.exe (the first build takes a few minutes)"
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

# --- 2. sing-box + wintun (downloaded unless provided) ---------------------------
$haveSb = $false
$haveWt = $false
if ($env:SINGBOX_DIR) {
  $p = Join-Path $env:SINGBOX_DIR "sing-box.exe"
  if (Test-Path $p) { Copy-Item $p $pkg; $haveSb = $true; Write-Host ">> using sing-box.exe from $($env:SINGBOX_DIR)" }
  $p = Join-Path $env:SINGBOX_DIR "wintun.dll"
  if (Test-Path $p) { Copy-Item $p $pkg; $haveWt = $true; Write-Host ">> using wintun.dll from $($env:SINGBOX_DIR)" }
}

if (-not $haveSb) {
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
}

if (-not $haveWt) {
  Write-Host ">> downloading wintun"
  Invoke-WebRequest "https://www.wintun.net/builds/wintun-0.14.1.zip" -OutFile "$tmp\wintun.zip"
  Expand-Archive "$tmp\wintun.zip" "$tmp\wintun" -Force
  Copy-Item "$tmp\wintun\wintun\bin\amd64\wintun.dll" $pkg
}
Write-Host ("   " + (& "$pkg\sing-box.exe" version | Select-Object -First 1) + "  (Full tunnel needs sing-box 1.12 or newer)")

# --- 3. docs + zip -------------------------------------------------------------------
if (Test-Path "$root\LICENSE") { Copy-Item "$root\LICENSE" "$pkg\LICENSE.txt" }
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
