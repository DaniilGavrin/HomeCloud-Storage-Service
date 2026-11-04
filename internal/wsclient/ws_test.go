package wsclient

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func serveHandshake(t *testing.T, action func(net.Conn, *bufio.Reader)) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			t.Error("missing RFC nonce")
			return
		}
		sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		hij, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijack")
			return
		}
		nc, rd, e := hij.Hijack()
		if e != nil {
			t.Error(e)
			return
		}
		defer nc.Close()
		io.WriteString(nc, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+base64.StdEncoding.EncodeToString(sum[:])+"\r\n\r\n")
		action(nc, rd.Reader)
	}))
	return s
}
func readMasked(t *testing.T, r *bufio.Reader) (byte, []byte) {
	t.Helper()
	var hdr [2]byte
	if _, e := io.ReadFull(r, hdr[:]); e != nil {
		t.Fatal(e)
	}
	if hdr[1]&0x80 == 0 {
		t.Fatal("unmasked client frame")
	}
	size := int(hdr[1] & 127)
	if size == 126 {
		var l [2]byte
		io.ReadFull(r, l[:])
		size = int(binary.BigEndian.Uint16(l[:]))
	}
	if size >= 65536 {
		t.Fatal("test frame too large")
	}
	var mask [4]byte
	io.ReadFull(r, mask[:])
	b := make([]byte, size)
	io.ReadFull(r, b)
	for i := range b {
		b[i] ^= mask[i%4]
	}
	return hdr[0] & 0xf, b
}
func TestControlHandshakeFrameAndPing(t *testing.T) {
	s := serveHandshake(t, func(n net.Conn, rd *bufio.Reader) {
		op, b := readMasked(t, rd)
		if op != 1 || string(b) != "hello" {
			t.Errorf("got %d %q", op, b)
			return
		}
		n.Write([]byte{0x89, 0x01, 'p'}) // Server Ping
		op, b = readMasked(t, rd)
		if op != 10 || string(b) != "p" {
			t.Errorf("pong %d %q", op, b)
		}
		n.Write([]byte{0x81, 0x05, 'w', 'o', 'r', 'l', 'd'})
		time.Sleep(50 * time.Millisecond)
	})
	defer s.Close()
	u := "ws" + strings.TrimPrefix(s.URL, "http")
	c, e := Dial(context.Background(), u)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e := c.WriteText(context.Background(), []byte("hello")); e != nil {
		t.Fatal(e)
	}
	b, e := c.ReadText(context.Background())
	if e != nil || string(b) != "world" {
		t.Fatalf("%q %v", b, e)
	}
}
func TestRejectPublicScheme(t *testing.T) {
	if _, e := Dial(context.Background(), "https://example.com"); e == nil {
		t.Fatal("unexpected acceptance")
	}
}

func TestPrivateIPPolicy(t *testing.T) {
	for _, q := range []struct {
		s    string
		want bool
	}{{"127.0.0.1", true}, {"192.168.39.11", true}, {"10.0.0.1", true}, {"100.82.22.45", true}, {"fd7a:115c:a1e0::1", true}, {"8.8.8.8", false}, {"1.1.1.1", false}} {
		ip, e := netip.ParseAddr(q.s)
		if e != nil {
			t.Fatal(e)
		}
		if privateIP(ip) != q.want {
			t.Fatalf("policy %s", q.s)
		}
	}
}
