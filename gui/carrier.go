package main

// Client-side carrier: a local SOCKS5 proxy (TCP + UDP) whose sessions ride a
// single WebSocket connection to the server, multiplexed with yamux. Adapted
// from the CLI into a Service that the GUI can start and stop.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
	xproxy "golang.org/x/net/proxy"
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
)

// ----------------------------------------------------------------- framing

func yamuxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 15 * time.Second
	c.ConnectionWriteTimeout = 20 * time.Second
	c.MaxStreamWindowSize = 1 << 22
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

// ----------------------------------------------------------------- carrier

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

func (c *carrier) close() {
	c.mu.Lock()
	if c.sess != nil {
		c.sess.Close()
		c.sess = nil
	}
	c.mu.Unlock()
}

// ----------------------------------------------------------------- service

// Service runs the local SOCKS5 listener for one profile until Stop. When full
// tunnel is enabled it also owns the sing-box TUN process (stopped via tunStop).
type Service struct {
	car     *carrier
	ln      net.Listener
	tunStop func() bool // returns false if the tunnel could not be torn down
}

func StartService(p *Profile) (*Service, error) {
	scheme := "ws"
	if p.TLS {
		scheme = "wss"
	}
	s := &Service{
		car: &carrier{url: scheme + "://" + net.JoinHostPort(p.Server, p.Port) + p.Path, token: p.Token},
	}
	ln, err := net.Listen("tcp", p.Socks)
	if err != nil {
		return nil, err
	}
	s.ln = ln
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go handleSocks(conn, s.car)
		}
	}()
	return s, nil
}

// Stop tears the tunnel down. For full tunnel it first stops sing-box and waits
// for it to exit (so system routes are restored) BEFORE closing the local SOCKS
// proxy — otherwise traffic is black-holed while the TUN is still up. If sing-box
// can't be stopped (e.g. the admin prompt was cancelled) it returns false and
// leaves everything running so the machine keeps its connectivity.
func (s *Service) Stop() bool {
	if s == nil {
		return true
	}
	if s.tunStop != nil {
		if !s.tunStop() {
			return false
		}
		s.tunStop = nil
	}
	if s.ln != nil {
		s.ln.Close()
	}
	if s.car != nil {
		s.car.close()
	}
	return true
}

// ExitIP fetches the public IP seen through the given SOCKS5 proxy.
func ExitIP(socksAddr string) (string, error) {
	d, err := xproxy.SOCKS5("tcp", socksAddr, nil, xproxy.Direct)
	if err != nil {
		return "", err
	}
	tr := &http.Transport{Dial: d.Dial}
	cl := &http.Client{Transport: tr, Timeout: 12 * time.Second}
	resp, err := cl.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(b)), nil
}

// ----------------------------------------------------------------- socks5

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
