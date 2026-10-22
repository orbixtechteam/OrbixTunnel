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
)

func findSingbox() string {
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p
	}
	var cands []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		cands = append(cands, filepath.Join(dir, "sing-box"), filepath.Join(dir, "sing-box.exe"))
	}
	cands = append(cands,
		"/opt/homebrew/opt/sing-box/bin/sing-box",
		"/usr/local/bin/sing-box",
		"/usr/bin/sing-box",
	)
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
		return errors.New("sing-box not found - install it (brew install sing-box) or place the binary next to the app")
	}
	cfg, err := writeSingboxConfig(p)
	if err != nil {
		return err
	}
	// macOS app double-clicked (not root): launch sing-box via an admin prompt.
	if runtime.GOOS == "darwin" && os.Geteuid() != 0 {
		return s.startTUNDarwinElevated(sb, cfg)
	}

	cmd := exec.Command(sb, "run", "-c", cfg)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.tunStop = func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	return nil
}

// startTUNDarwinElevated runs sing-box as root through an osascript admin
// prompt (so the double-clicked .app can bring up the TUN), records the pid,
// and stops it with another admin prompt.
func (s *Service) startTUNDarwinElevated(sb, cfg string) error {
	// Kill any previous instance via its pidfile (robust across app relaunches),
	// then start fresh and record the new pid. A pidfile avoids `pkill -f`, which
	// would also match this launcher's own command line (it contains the config
	// path) and kill itself.
	shell := fmt.Sprintf(
		"OLD=$(cat /tmp/evpn-sb.pid 2>/dev/null); kill $OLD 2>/dev/null; %s run -c %s </dev/null >/tmp/evpn-sb.log 2>&1 & echo $! > /tmp/evpn-sb.pid",
		sb, cfg)
	start := fmt.Sprintf(`do shell script "%s" with administrator privileges`, shell)
	if err := exec.Command("osascript", "-e", start).Run(); err != nil {
		return fmt.Errorf("admin launch failed (sing-box): %v", err)
	}
	s.tunStop = func() {
		stop := `do shell script "kill $(cat /tmp/evpn-sb.pid 2>/dev/null) 2>/dev/null; rm -f /tmp/evpn-sb.pid" with administrator privileges`
		exec.Command("osascript", "-e", stop).Run()
	}
	return nil
}
