package main

// eVPN carrier - single-file edition (server + client + config generator).
//
//   evpn server [-peers FILE]          run on the VPS (looks like a telemetry API)
//   evpn gen -name N -server HOST ...   generate a client profile + add its token to peers
//   evpn client -c PROFILE.evpn         import a profile and expose a local SOCKS5 proxy
//
// One WebSocket connection carries a yamux session. Each SOCKS request becomes
// one yamux stream. A stream starts with a 1-byte command:
//
//   cmdTCP: [cmdTCP][addr]   then server replies [status], then raw bytes both ways
//   cmdUDP: [cmdUDP]         then length-prefixed datagrams both ways: [uint16 len][addr][data]
//
// addr is a compact SOCKS-like address: [atyp][addr bytes][uint16 port].
//
// Auth model (WireGuard-like): the server authorizes any token listed in its
// peers file (one "token name" per line). `evpn gen` creates a random token,
// appends it to that file, and writes a client profile carrying the same token.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

const (
	cmdTCP byte = 1
	cmdUDP byte = 3

	atypIPv4   byte = 1
	atypDomain byte = 3
	atypIPv6   byte = 4

	statusOK   byte = 0
	statusFail byte = 1

	defaultPath = "/api/v2/telemetry/ingest"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "server":
		runServer(os.Args[2:])
	case "client":
		runClient(os.Args[2:])
	case "gen":
		runGen(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  evpn server [-peers FILE] [-port 8443] [-path /api/...]
  evpn gen -name NAME -server HOST [-port 8443] [-path /...] [-tls] [-peers FILE] [-out FILE]
  evpn client -c PROFILE.evpn`)
	os.Exit(1)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// ------------------------------------------------------------------ protocol

func yamuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 15 * time.Second
	c.ConnectionWriteTimeout = 20 * time.Second
	c.MaxStreamWindowSize = 1 << 22 // 4 MiB per stream
	c.LogOutput = io.Discard
	return c
}

func writeAddr(w io.Writer, host string, port uint16) error {
	var buf []byte
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			buf = append(buf, atypIPv4)
			buf = append(buf, ip4...)
		} else {
			buf = append(buf, atypIPv6)
			buf = append(buf, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("host too long")
		}
		buf = append(buf, atypDomain, byte(len(host)))
		buf = append(buf, host...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], port)
	buf = append(buf, p[:]...)
	_, err := w.Write(buf)
	return err
}

func readAddr(r io.Reader) (string, uint16, error) {
	var a [1]byte
	if _, err := io.ReadFull(r, a[:]); err != nil {
		return "", 0, err
	}
	var host string
	switch a[0] {
	case atypIPv4:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(b[:]).String()
	case atypIPv6:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(b[:]).String()
	case atypDomain:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", 0, err
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, errors.New("bad atyp")
	}
	var p [2]byte
	if _, err := io.ReadFull(r, p[:]); err != nil {
		return "", 0, err
	}
	return host, binary.BigEndian.Uint16(p[:]), nil
}

func writeDatagram(w io.Writer, host string, port uint16, data []byte) error {
	var inner bytes.Buffer
	if err := writeAddr(&inner, host, port); err != nil {
		return err
	}
	inner.Write(data)
	if inner.Len() > 0xffff {
		return errors.New("datagram too large")
	}
	var lp [2]byte
	binary.BigEndian.PutUint16(lp[:], uint16(inner.Len()))
	if _, err := w.Write(lp[:]); err != nil {
		return err
	}
	_, err := w.Write(inner.Bytes())
	return err
}

func readDatagram(r io.Reader) (host string, port uint16, data []byte, err error) {
	var lp [2]byte
	if _, err = io.ReadFull(r, lp[:]); err != nil {
		return
	}
	block := make([]byte, binary.BigEndian.Uint16(lp[:]))
	if _, err = io.ReadFull(r, block); err != nil {
		return
	}
	br := bytes.NewReader(block)
	if host, port, err = readAddr(br); err != nil {
		return
	}
	data = make([]byte, br.Len())
	_, err = io.ReadFull(br, data)
	return
}

func hostport(host string, port uint16) string {
	return net.JoinHostPort(host, strconv.Itoa(int(port)))
}

func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); a.Close(); done <- struct{}{} }()
	go func() { io.Copy(b, a); b.Close(); done <- struct{}{} }()
	<-done
	<-done
}

// --------------------------------------------------------------- profile I/O

type profile struct {
	name, server, port, path, token, socks string
	tls                                     bool
}

func parseProfile(path string) (*profile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p := &profile{port: "8443", path: defaultPath, socks: "127.0.0.1:1080"}
	sc := bufio.NewScanner(f)
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
			p.name = v
		case "server":
			p.server = v
		case "port":
			p.port = v
		case "path":
			p.path = v
		case "token":
			p.token = v
		case "socks":
			p.socks = v
		case "tls":
			p.tls = v == "true" || v == "1"
		}
	}
	return p, sc.Err()
}

func loadPeers(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		m[fields[0]] = strings.Join(fields[1:], " ")
	}
	return m, sc.Err()
}

// ------------------------------------------------------------------ gen mode

func runGen(args []string) {
	fs := flag.NewFlagSet("gen", flag.ExitOnError)
	name := fs.String("name", "client", "client name")
	server := fs.String("server", "", "server host or IP (required)")
	port := fs.String("port", "8443", "server port")
	path := fs.String("path", defaultPath, "endpoint path")
	tls := fs.Bool("tls", false, "use wss (TLS)")
	peers := fs.String("peers", envOr("EVPN_PEERS", "/etc/orbi/peers.txt"), "peers file to append the token to")
	out := fs.String("out", "", "output profile file (default: stdout)")
	socks := fs.String("socks", "127.0.0.1:1080", "local SOCKS listen for the client")
	fs.Parse(args)

	if *server == "" {
		fmt.Fprintln(os.Stderr, "gen: -server is required")
		os.Exit(1)
	}

	token := randHex(32)

	pf, err := os.OpenFile(*peers, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(pf, "%s %s\n", token, *name)
	pf.Close()

	tlsStr := "false"
	if *tls {
		tlsStr = "true"
	}
	prof := fmt.Sprintf(`# eVPN profile - %s
name = %s
server = %s
port = %s
path = %s
tls = %s
token = %s
socks = %s
`, *name, *name, *server, *port, *path, tlsStr, token, *socks)

	if *out != "" {
		if err := os.WriteFile(*out, []byte(prof), 0600); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "wrote profile %s (token for %q added to %s)\n", *out, *name, *peers)
	} else {
		fmt.Print(prof)
		fmt.Fprintf(os.Stderr, "\n# token for %q added to %s\n", *name, *peers)
	}
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		log.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// -------------------------------------------------------------------- server

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	peersFlag := fs.String("peers", envOr("EVPN_PEERS", ""), "peers file (one \"token name\" per line)")
	portFlag := fs.String("port", envOr("EVPN_PORT", "8443"), "listen port")
	pathFlag := fs.String("path", envOr("EVPN_PATH", defaultPath), "carrier endpoint path (must match the client profile)")
	tokenFlag := fs.String("token", envOr("EVPN_TOKEN", ""), "single shared token (only used when no -peers file is set)")
	appFlag := fs.String("app", envOr("EVPN_APP_NAME", "telemetry"), "app name shown in the decoy JSON response")
	fs.Parse(args)
	fakeApp = *appFlag

	port := *portFlag
	path := *pathFlag
	staticToken := *tokenFlag
	peersPath := *peersFlag
	if peersPath == "" && staticToken == "" {
		log.Fatal("no auth configured: set -peers FILE (EVPN_PEERS) or -token VALUE (EVPN_TOKEN)")
	}

	// authorized reports whether a token may open the carrier. With a peers
	// file, only listed tokens are accepted (re-read per connection, so new
	// peers work without a restart); otherwise the single EVPN_TOKEN is used.
	authorized := func(tok string) bool {
		if tok == "" {
			return false
		}
		if peersPath != "" {
			peers, err := loadPeers(peersPath)
			if err != nil {
				return false
			}
			_, ok := peers[tok]
			return ok
		}
		return tok == staticToken
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			serveFake(w)
			return
		}
		tok := r.Header.Get("X-Evpn-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		if !authorized(tok) {
			serveFake(w) // same boring response on bad auth - no signal
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(-1)
		nc := websocket.NetConn(context.Background(), c, websocket.MessageBinary)
		sess, err := yamux.Server(nc, yamuxConfig())
		if err != nil {
			c.Close(websocket.StatusInternalError, "")
			return
		}
		handleSession(sess)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { serveFake(w) })

	if peersPath != "" {
		log.Printf("[orbi] carrier listening on :%s path=%s peers=%s", port, path, peersPath)
	} else {
		log.Printf("[orbi] carrier listening on :%s path=%s (single token)", port, path)
	}
	log.Fatal((&http.Server{Addr: ":" + port, Handler: mux}).ListenAndServe())
}

// fakeApp is the "app" value in the decoy response (set via -app / EVPN_APP_NAME).
var fakeApp = "telemetry"

func serveFake(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	b, _ := json.Marshal(struct {
		OK  bool   `json:"ok"`
		App string `json:"app"`
	}{true, fakeApp})
	w.Write(b)
}

func handleSession(sess *yamux.Session) {
	defer sess.Close()
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			return
		}
		go handleStream(stream)
	}
}

func handleStream(stream net.Conn) {
	defer stream.Close()
	var cmd [1]byte
	if _, err := io.ReadFull(stream, cmd[:]); err != nil {
		return
	}
	switch cmd[0] {
	case cmdTCP:
		handleTCP(stream)
	case cmdUDP:
		handleUDP(stream)
	}
}

func handleTCP(stream net.Conn) {
	host, port, err := readAddr(stream)
	if err != nil {
		return
	}
	up, err := net.DialTimeout("tcp", hostport(host, port), 15*time.Second)
	if err != nil {
		stream.Write([]byte{statusFail})
		return
	}
	defer up.Close()
	if t, ok := up.(*net.TCPConn); ok {
		t.SetNoDelay(true)
	}
	if _, err := stream.Write([]byte{statusOK}); err != nil {
		return
	}
	pipe(stream, up)
}

func handleUDP(stream net.Conn) {
	var wmu sync.Mutex
	var cmu sync.Mutex
	conns := make(map[string]*net.UDPConn)
	defer func() {
		cmu.Lock()
		for _, c := range conns {
			c.Close()
		}
		cmu.Unlock()
	}()

	for {
		host, port, data, err := readDatagram(stream)
		if err != nil {
			return
		}
		key := hostport(host, port)

		cmu.Lock()
		uc := conns[key]
		if uc == nil {
			raddr, err := net.ResolveUDPAddr("udp", key)
			if err != nil {
				cmu.Unlock()
				continue
			}
			uc, err = net.DialUDP("udp", nil, raddr)
			if err != nil {
				cmu.Unlock()
				continue
			}
			conns[key] = uc
			go func(uc *net.UDPConn, host string, port uint16) {
				buf := make([]byte, 64*1024)
				for {
					uc.SetReadDeadline(time.Now().Add(60 * time.Second))
					n, _, err := uc.ReadFromUDP(buf)
					if err != nil {
						return
					}
					wmu.Lock()
					werr := writeDatagram(stream, host, port, buf[:n])
					wmu.Unlock()
					if werr != nil {
						return
					}
				}
			}(uc, host, port)
		}
		cmu.Unlock()
		uc.Write(data)
	}
}

// -------------------------------------------------------------------- client

type carrier struct {
	url   string
	token string
	mu    sync.Mutex
	sess  *yamux.Session
}

func (c *carrier) session() (*yamux.Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil && !c.sess.IsClosed() {
		return c.sess, nil
	}
	h := http.Header{}
	h.Set("X-Evpn-Token", c.token)
	h.Set("User-Agent", browserUA)
	wsc, _, err := websocket.Dial(context.Background(), c.url, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		return nil, err
	}
	wsc.SetReadLimit(-1)
	nc := websocket.NetConn(context.Background(), wsc, websocket.MessageBinary)
	sess, err := yamux.Client(nc, yamuxConfig())
	if err != nil {
		wsc.Close(websocket.StatusInternalError, "")
		return nil, err
	}
	c.sess = sess
	return sess, nil
}

func (c *carrier) open() (net.Conn, error) {
	for i := 0; i < 3; i++ {
		sess, err := c.session()
		if err != nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		st, err := sess.OpenStream()
		if err != nil {
			c.mu.Lock()
			if c.sess == sess {
				c.sess = nil
			}
			c.mu.Unlock()
			continue
		}
		return st, nil
	}
	return nil, errors.New("carrier unavailable")
}

func runClient(args []string) {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	cfg := fs.String("c", "", "profile file to import (evpn gen output)")
	fs.Parse(args)

	listen := envOr("EVPN_SOCKS", "127.0.0.1:1080")
	scheme := "ws"
	host := envOr("EVPN_SERVER_HOST", "127.0.0.1")
	port := envOr("EVPN_SERVER_PORT", "8443")
	path := envOr("EVPN_PATH", defaultPath)
	token := envOr("EVPN_TOKEN", "")

	if *cfg != "" {
		p, err := parseProfile(*cfg)
		if err != nil {
			log.Fatal(err)
		}
		if p.server != "" {
			host = p.server
		}
		if p.port != "" {
			port = p.port
		}
		if p.path != "" {
			path = p.path
		}
		if p.token != "" {
			token = p.token
		}
		if p.socks != "" {
			listen = p.socks
		}
		if p.tls {
			scheme = "wss"
		}
	}
	if os.Getenv("EVPN_TLS") == "1" {
		scheme = "wss"
	}

	if token == "" {
		log.Fatal("no token: use a profile (-c FILE) or set EVPN_TOKEN")
	}

	car := &carrier{url: scheme + "://" + net.JoinHostPort(host, port) + path, token: token}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("[orbi] SOCKS5 %s  ->  %s", listen, car.url)
	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go handleSocks(conn, car)
	}
}

func handleSocks(conn net.Conn, car *carrier) {
	defer conn.Close()
	if t, ok := conn.(*net.TCPConn); ok {
		t.SetNoDelay(true)
	}
	br := bufio.NewReader(conn)

	ver, err := br.ReadByte()
	if err != nil || ver != 5 {
		return
	}
	n, err := br.ReadByte()
	if err != nil {
		return
	}
	if _, err := io.CopyN(io.Discard, br, int64(n)); err != nil {
		return
	}
	conn.Write([]byte{5, 0})

	hdr := make([]byte, 4)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[0] != 5 {
		return
	}
	host, port, err := readSocksAddr(br, hdr[3])
	if err != nil {
		return
	}

	switch hdr[1] {
	case 1:
		socksTCP(conn, br, car, host, port)
	case 3:
		socksUDP(conn, car)
	default:
		conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
	}
}

func socksTCP(conn net.Conn, br *bufio.Reader, car *carrier, host string, port uint16) {
	st, err := car.open()
	if err != nil {
		conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer st.Close()

	st.Write([]byte{cmdTCP})
	if err := writeAddr(st, host, port); err != nil {
		conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	var status [1]byte
	if _, err := io.ReadFull(st, status[:]); err != nil || status[0] != statusOK {
		conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})

	done := make(chan struct{}, 2)
	go func() { io.Copy(st, br); st.Close(); done <- struct{}{} }()
	go func() { io.Copy(conn, st); conn.Close(); done <- struct{}{} }()
	<-done
	<-done
}

func socksUDP(conn net.Conn, car *carrier) {
	uc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		conn.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer uc.Close()

	la := uc.LocalAddr().(*net.UDPAddr)
	conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, byte(la.Port >> 8), byte(la.Port)})

	st, err := car.open()
	if err != nil {
		return
	}
	defer st.Close()
	st.Write([]byte{cmdUDP})

	var camu sync.Mutex
	var clientAddr *net.UDPAddr

	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, raddr, err := uc.ReadFromUDP(buf)
			if err != nil {
				st.Close()
				return
			}
			camu.Lock()
			clientAddr = raddr
			camu.Unlock()

			pkt := buf[:n]
			if len(pkt) < 4 || pkt[2] != 0 {
				continue
			}
			r := bytes.NewReader(pkt[3:])
			host, port, err := readAddr(r)
			if err != nil {
				continue
			}
			data := make([]byte, r.Len())
			io.ReadFull(r, data)
			if writeDatagram(st, host, port, data) != nil {
				return
			}
		}
	}()

	go func() {
		for {
			host, port, data, err := readDatagram(st)
			if err != nil {
				uc.Close()
				return
			}
			camu.Lock()
			ca := clientAddr
			camu.Unlock()
			if ca == nil {
				continue
			}
			var out bytes.Buffer
			out.Write([]byte{0, 0, 0})
			writeAddr(&out, host, port)
			out.Write(data)
			uc.WriteToUDP(out.Bytes(), ca)
		}
	}()

	io.Copy(io.Discard, conn)
}

func readSocksAddr(r io.Reader, atyp byte) (string, uint16, error) {
	var host string
	switch atyp {
	case 1:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(b[:]).String()
	case 4:
		var b [16]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return "", 0, err
		}
		host = net.IP(b[:]).String()
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", 0, err
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(r, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	default:
		return "", 0, errors.New("bad atyp")
	}
	var p [2]byte
	if _, err := io.ReadFull(r, p[:]); err != nil {
		return "", 0, err
	}
	return host, binary.BigEndian.Uint16(p[:]), nil
}
