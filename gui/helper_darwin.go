//go:build darwin

package main

// macOS root helper. Bringing up a TUN needs root. Instead of asking for the
// admin password on every connect/disconnect, the app asks ONCE per launch to
// start a small root supervisor script. The app then talks to it over a FIFO
// in a private (0700) temp dir: "start <config>", "stop", "quit". The supervisor
// runs sing-box, flushes the DNS cache on start/stop, and tears everything down
// by itself if the app exits or crashes.
//
// Trade-off: while the app is open, another process running as the same user
// could also drive this supervisor. The proper long-term fix is a signed
// privileged helper (SMAppService) or a Network Extension.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const helperScript = `#!/bin/bash
CTL="$1"; APP="$2"; SB="$3"
RCFG=/var/run/orbix-sb.json
SBPID=""

ack() {
  rm -f "$CTL/ack.tmp"
  ( set -C; printf '%s\n' "$1" > "$CTL/ack.tmp" ) && mv -f "$CTL/ack.tmp" "$CTL/ack"
}
flush() { dscacheutil -flushcache; killall -HUP mDNSResponder; }
stop_sb() {
  if [ -n "$SBPID" ]; then
    kill "$SBPID" 2>/dev/null
    for i in $(seq 1 25); do
      kill -0 "$SBPID" 2>/dev/null || break
      sleep 0.2
    done
    kill -0 "$SBPID" 2>/dev/null && kill -9 "$SBPID" 2>/dev/null
    SBPID=""
  fi
  flush
}
cleanup() { stop_sb; rm -f /var/run/orbix-sup.sh "$RCFG" "$CTL/sup.pid"; }
trap 'cleanup; exit 0' TERM INT HUP

echo $$ > "$CTL/sup.pid"
exec 3<> "$CTL/cmd"
while kill -0 "$APP" 2>/dev/null; do
  if read -r -t 1 -u 3 line; then
    case "$line" in
      "start "*)
        stop_sb
        if ! cp "${line#start }" "$RCFG"; then ack "error: cannot read config"; continue; fi
        flush
        "$SB" run -c "$RCFG" </dev/null >"$CTL/sb.log" 2>&1 &
        SBPID=$!
        sleep 1
        if kill -0 "$SBPID" 2>/dev/null; then ack ok; else SBPID=""; ack "error: sing-box exited right after start"; fi
        ;;
      stop) stop_sb; ack ok ;;
      quit) ack ok; cleanup; exit 0 ;;
    esac
  fi
done
cleanup
`

type rootHelper struct {
	mu  sync.Mutex
	dir string
}

var helper rootHelper

func (h *rootHelper) alive() bool {
	if h.dir == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(h.dir, "sup.pid"))
	if err != nil {
		return false
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid <= 0 {
		return false
	}
	err = syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM // EPERM: exists, but is root's
}

// ensure starts the root supervisor (one admin prompt) if it isn't running.
func (h *rootHelper) ensure(sb string) error {
	if h.alive() {
		return nil
	}
	if h.dir != "" {
		os.RemoveAll(h.dir)
		h.dir = ""
	}
	dir, err := os.MkdirTemp("", "orbix-ctl-")
	if err != nil {
		return err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		os.RemoveAll(dir)
		return err
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "cmd"), 0600); err != nil {
		os.RemoveAll(dir)
		return err
	}
	// The script travels inside the elevated command itself (base64), so there
	// is no user-writable file that root could be tricked into running.
	b64 := base64.StdEncoding.EncodeToString([]byte(helperScript))
	shell := fmt.Sprintf(
		"echo %s | base64 -D > /var/run/orbix-sup.sh && chmod 700 /var/run/orbix-sup.sh && nohup /bin/bash /var/run/orbix-sup.sh %s %d %s </dev/null >/dev/null 2>&1 &",
		b64, shQuote(dir), os.Getpid(), shQuote(sb))
	script := fmt.Sprintf(`do shell script "%s" with administrator privileges`, asEscape(shell))
	if err := exec.Command("osascript", "-e", script).Run(); err != nil {
		os.RemoveAll(dir)
		return err
	}
	h.dir = dir
	for i := 0; i < 80; i++ { // wait up to 8s for the supervisor to report in
		if h.alive() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("root helper did not start")
}

func (h *rootHelper) lastLog() string {
	b, err := os.ReadFile(filepath.Join(h.dir, "sb.log"))
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[len(s)-400:]
	}
	return s
}

// send writes one command line and waits for the supervisor's acknowledgement.
func (h *rootHelper) send(line string, timeout time.Duration) error {
	if h.dir == "" {
		return errors.New("root helper is not running")
	}
	ack := filepath.Join(h.dir, "ack")
	os.Remove(ack)
	f, err := os.OpenFile(filepath.Join(h.dir, "cmd"), os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return errors.New("root helper is not running")
	}
	_, err = f.WriteString(line + "\n")
	f.Close()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(ack); err == nil && len(b) > 0 {
			msg := strings.TrimSpace(string(b))
			if msg == "ok" {
				return nil
			}
			if l := h.lastLog(); l != "" {
				msg += ": " + l
			}
			return errors.New(strings.TrimPrefix(msg, "error: "))
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("timed out waiting for the root helper")
}

// startTUNDarwin brings up sing-box through the root supervisor.
func (s *Service) startTUNDarwin(sb, cfg string) error {
	helper.mu.Lock()
	defer helper.mu.Unlock()
	if err := helper.ensure(sb); err != nil {
		return fmt.Errorf("admin launch failed: %v", err)
	}
	if err := helper.send("start "+cfg, 20*time.Second); err != nil {
		return fmt.Errorf("sing-box: %v", err)
	}
	s.tunStop = func() bool {
		helper.mu.Lock()
		defer helper.mu.Unlock()
		if !helper.alive() {
			return true // supervisor already cleaned up
		}
		return helper.send("stop", 15*time.Second) == nil
	}
	return nil
}

// shutdownHelper ends the supervisor (called when the app quits).
func shutdownHelper() {
	helper.mu.Lock()
	defer helper.mu.Unlock()
	if helper.alive() {
		helper.send("quit", 5*time.Second)
	}
	if helper.dir != "" {
		os.RemoveAll(helper.dir)
		helper.dir = ""
	}
}
