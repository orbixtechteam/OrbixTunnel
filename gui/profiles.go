package main

// Profile storage: imported .evpn files are copied into a per-user config
// directory so the app keeps a list of tunnels across restarts.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Profile struct {
	Name   string
	Server string
	Port   string
	Path   string
	Token  string
	Socks  string
	TLS    bool
	File   string // path on disk
}

func profilesDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "evpn", "profiles")
	os.MkdirAll(dir, 0700)
	return dir
}

func parseProfileBytes(name string, data []byte) *Profile {
	p := &Profile{Name: name, Port: "8443", Path: "/api/v2/telemetry/ingest", Socks: "127.0.0.1:1080"}
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "name":
			p.Name = v
		case "server":
			p.Server = v
		case "port":
			p.Port = v
		case "path":
			p.Path = v
		case "token":
			p.Token = v
		case "socks":
			p.Socks = v
		case "tls":
			p.TLS = v == "true" || v == "1"
		}
	}
	return p
}

func loadProfile(file string) (*Profile, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	p := parseProfileBytes(name, data)
	p.File = file
	if p.Server == "" || p.Token == "" {
		return nil, errors.New("profile missing server or token")
	}
	return p, nil
}

func listProfiles() []*Profile {
	dir := profilesDir()
	entries, _ := os.ReadDir(dir)
	var out []*Profile
	for _, e := range entries {
		if e.IsDir() || !isProfileFile(e.Name()) {
			continue
		}
		if p, err := loadProfile(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// importProfile copies a .evpn file into the store, keyed by its name (so the
// same tunnel re-imported overwrites rather than duplicates).
func importProfile(srcFile string) (*Profile, error) {
	data, err := os.ReadFile(srcFile)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSuffix(filepath.Base(srcFile), filepath.Ext(srcFile))
	p := parseProfileBytes(name, data)
	if p.Server == "" || p.Token == "" {
		return nil, errors.New("not a valid eVPN profile (missing server or token)")
	}
	dst := filepath.Join(profilesDir(), sanitize(p.Name)+".orb")
	if err := os.WriteFile(dst, data, 0600); err != nil {
		return nil, err
	}
	p.File = dst
	return p, nil
}

// saveProfile writes a profile entered by hand into the store as a .orb file.
func saveProfile(p *Profile) (*Profile, error) {
	if p.Server == "" || p.Token == "" {
		return nil, errors.New("server and token are required")
	}
	if p.Name == "" {
		p.Name = "profile"
	}
	if p.Port == "" {
		p.Port = "8443"
	}
	if p.Path == "" {
		p.Path = "/api/v2/telemetry/ingest"
	}
	if p.Socks == "" {
		p.Socks = "127.0.0.1:1080"
	}
	tls := "false"
	if p.TLS {
		tls = "true"
	}
	body := fmt.Sprintf("# Orbi-VPN profile - %s\nname = %s\nserver = %s\nport = %s\npath = %s\ntls = %s\ntoken = %s\nsocks = %s\n",
		p.Name, p.Name, p.Server, p.Port, p.Path, tls, p.Token, p.Socks)
	dst := filepath.Join(profilesDir(), sanitize(p.Name)+".orb")
	if err := os.WriteFile(dst, []byte(body), 0600); err != nil {
		return nil, err
	}
	p.File = dst
	return p, nil
}

func deleteProfile(p *Profile) error {
	if p == nil || p.File == "" {
		return nil
	}
	return os.Remove(p.File)
}

func isProfileFile(name string) bool {
	return strings.HasSuffix(name, ".orb") || strings.HasSuffix(name, ".evpn")
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "profile"
	}
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "-", "..", "_")
	return r.Replace(s)
}
