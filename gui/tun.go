package main

// Full-tunnel support: the app drives sing-box as its TUN engine. We generate
// a sing-box config pointed at our in-process SOCKS proxy (fakeip DNS so names
// resolve server-side, UDP allowed for QUIC, the server IP kept direct to avoid
// a routing loop) and run sing-box as a child process. Requires admin/root.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// findSingbox prefers the copy bundled next to the app executable (inside
// Orbix Tunnel.app/Contents/MacOS on macOS), then PATH, then common install dirs.
func findSingbox() string {
	var cands []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, "sing-box"), filepath.Join(dir, "sing-box.exe"))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p
	}
	cands = []string{
		"/opt/homebrew/opt/sing-box/bin/sing-box",
		"/usr/local/bin/sing-box",
		"/usr/bin/sing-box",
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// resolveExcludes returns CIDRs for the server address, so its traffic stays
// direct instead of looping back into the tunnel.
func resolveExcludes(server string) []string {
	if ip := net.ParseIP(server); ip != nil {
		if ip.To4() != nil {
			return []string{server + "/32"}
		}
		return []string{server + "/128"}
	}
	var out []string
	ips, _ := net.LookupIP(server)
	for _, ip := range ips {
		if ip.To4() != nil {
			out = append(out, ip.String()+"/32")
		} else {
			out = append(out, ip.String()+"/128")
		}
	}
	return out
}

func quoteJoin(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = `"` + s + `"`
	}
	return strings.Join(q, ", ")
}

func writeSingboxConfig(p *Profile) (string, error) {
	shost, sport, err := net.SplitHostPort(p.Socks)
	if err != nil {
		shost, sport = "127.0.0.1", "1080"
	}

	excludes := resolveExcludes(p.Server)
	excludeArr := "[" + quoteJoin(excludes) + "]"
	directArr := "[" + quoteJoin(append([]string{"127.0.0.0/8"}, excludes...)) + "]"

	cacheDir, _ := os.UserCacheDir()
	if cacheDir == "" {
		cacheDir = os.TempDir()
	}
	cachePath := filepath.Join(cacheDir, "evpn-fakeip.db")

	cfg := fmt.Sprintf(`{
  "log": { "level": "warn" },
  "experimental": { "cache_file": { "enabled": true, "store_fakeip": true, "path": %q } },
  "dns": {
    "servers": [
      { "type": "local", "tag": "local-dns" },
      { "type": "fakeip", "tag": "fakeip", "inet4_range": "198.18.0.0/15" }
    ],
    "rules": [
      { "query_type": ["A"], "action": "route", "server": "fakeip" },
      { "query_type": ["AAAA", 64, 65], "action": "reject" }
    ],
    "final": "local-dns"
  },
  "inbounds": [
    { "type": "tun", "tag": "tun-in", "address": ["172.19.0.1/30"], "mtu": 1400,
      "auto_route": true, "strict_route": true, "stack": "gvisor",
      "route_exclude_address": %s }
  ],
  "outbounds": [
    { "type": "socks", "tag": "evpn-out", "server": %q, "server_port": %s, "version": "5" },
    { "type": "direct", "tag": "direct" }
  ],
  "route": {
    "default_domain_resolver": "local-dns",
    "rules": [
      { "action": "sniff", "timeout": "1s" },
      { "protocol": "dns", "action": "hijack-dns" },
      { "ip_cidr": %s, "action": "route", "outbound": "direct" }
    ],
    "final": "evpn-out",
    "auto_detect_interface": true
  }
}`, cachePath, excludeArr, shost, sport, directArr)

	path := filepath.Join(os.TempDir(), "evpn-singbox.json")
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// StartTUN launches sing-box against the generated config and records how to
// stop it on the Service.
func (s *Service) StartTUN(p *Profile) error {
	sb := findSingbox()
	if sb == "" {
		return errors.New("sing-box not found - rebuild the app with scripts/package-mac.sh (it bundles sing-box), or install it (brew install sing-box)")
	}
	cfg, err := writeSingboxConfig(p)
	if err != nil {
		return err
	}
	// macOS app double-clicked (not root): sing-box runs under a root helper
	// that is started with a single admin prompt per app launch (helper_darwin.go).
	if runtime.GOOS == "darwin" && os.Geteuid() != 0 {
		return s.startTUNDarwin(sb, cfg)
	}

	flushDNS()
	cmd := exec.Command(sb, "run", "-c", cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.tunStop = func() bool {
		if cmd.Process != nil {
			cmd.Process.Signal(os.Interrupt) // let sing-box restore routes
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				cmd.Process.Kill()
			}
		}
		flushDNS()
		return true
	}
	return nil
}

// shQuote single-quotes s for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// asEscape escapes s for use inside an AppleScript double-quoted string.
func asEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// flushDNS drops cached fake-IP (198.18.0.0/15) answers after the tunnel is
// gone; otherwise apps keep resolving names to dead addresses for a while and
// the internet looks "down" right after disconnect.
func flushDNS() {
	switch runtime.GOOS {
	case "windows":
		exec.Command("ipconfig", "/flushdns").Run()
	case "linux":
		exec.Command("resolvectl", "flush-caches").Run()
	}
}
