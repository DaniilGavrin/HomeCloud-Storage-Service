// Package wsclient contains a small RFC6455 text WebSocket client built only
// on Go's standard library. It is for the private HomeCloud control channel.
// Sec-WebSocket-Key and client frame masks are mandatory RFC6455 random nonces,
// NOT authentication keys or access credentials.
package wsclient

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const MaxMessageSize = 1 << 20

var ErrProtocol = errors.New("WEBSOCKET_PROTOCOL_ERROR")

type Conn struct {
	raw    net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
}

func Dial(ctx context.Context, rawURL string) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("only ws:// private addresses are supported")
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	host := u.Hostname()
	if host == "" {
		return nil, ErrProtocol
	}
	address := net.JoinHostPort(host, port)
	raw, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	// A no-auth control channel must never be dialled to a public IP.
	if addr, ok := raw.RemoteAddr().(*net.TCPAddr); ok {
		ip, ok := netip.AddrFromSlice(addr.IP)
		if !ok || !privateIP(ip.Unmap()) {
			_ = raw.Close()
			return nil, errors.New("PUBLIC_CONTROL_ADDRESS_REJECTED")
		}
	}
	success := false
	defer func() {
		if !success {
			_ = raw.Close()
		}
	}()
	dead := time.Now().Add(9 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(dead) {
		dead = dl
	}
	_ = raw.SetDeadline(dead)
	var keybytes [16]byte
	if _, err = rand.Read(keybytes[:]); err != nil {
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keybytes[:])

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Sec-WebSocket-Key", key)
	request.Header.Set("Sec-WebSocket-Version", "13")
	if err = request.Write(raw); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(raw, 4096)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	sha := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(sha[:])
	if response.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") || !headerToken(response.Header.Get("Connection"), "upgrade") || response.Header.Get("Sec-WebSocket-Accept") != accept {
		return nil, fmt.Errorf("unexpected WebSocket upgrade: HTTP %d", response.StatusCode)
	}
	_ = raw.SetDeadline(time.Time{})
	success = true
	return &Conn{raw: raw, reader: reader}, nil
}
func headerToken(s, token string) bool {
	for _, v := range strings.Split(s, ",") {
		if strings.EqualFold(strings.TrimSpace(v), token) {
			return true
		}
	}
	return false
}
func (c *Conn) Close() error { return c.raw.Close() }

// WriteText sends one client-masked text frame. Only one writer uses the TCP
// connection at a time; a read goroutine may answer server ping with Pong.
func (c *Conn) WriteText(ctx context.Context, b []byte) error { return c.writeFrame(ctx, 1, b) }
func (c *Conn) writeFrame(ctx context.Context, opcode byte, b []byte) error {
	if len(b) > MaxMessageSize || (opcode >= 8 && len(b) > 125) {
		return ErrProtocol
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	dl := time.Now().Add(8 * time.Second)
	if when, ok := ctx.Deadline(); ok && when.Before(dl) {
		dl = when
	}
	if err := c.raw.SetWriteDeadline(dl); err != nil {
		return err
	}
	defer c.raw.SetWriteDeadline(time.Time{})
	var hdr [14]byte
	hdr[0] = 0x80 | opcode
	n := 2
	if len(b) < 126 {
		hdr[1] = 0x80 | byte(len(b))
	} else if len(b) <= 65535 {
		hdr[1] = 0x80 | 126
		binary.BigEndian.PutUint16(hdr[2:4], uint16(len(b)))
		n = 4
	} else {
		hdr[1] = 0x80 | 127
		binary.BigEndian.PutUint64(hdr[2:10], uint64(len(b)))
		n = 10
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	copy(hdr[n:n+4], mask[:])
	n += 4
	if err := writeAll(c.raw, hdr[:n]); err != nil {
		return err
	}
	payload := make([]byte, len(b))
	for i := range b {
		payload[i] = b[i] ^ mask[i%4]
	}
	return writeAll(c.raw, payload)
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

// ReadText understands Ping/Pong/Close and fragmented server text messages.
// Servers MUST NOT mask frames. It bounds aggregate payload before allocation.
func (c *Conn) ReadText(ctx context.Context) ([]byte, error) {
	var buf []byte
	fragmented := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(75 * time.Second)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		if err := c.raw.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		var header [2]byte
		if _, err := io.ReadFull(c.reader, header[:]); err != nil {
			return nil, err
		}
		if header[0]&0x70 != 0 || header[1]&0x80 != 0 {
			return nil, ErrProtocol
		}
		fin := header[0]&0x80 != 0
		opcode := header[0] & 0x0f
		size := uint64(header[1] & 0x7f)
		if size == 126 {
			var x [2]byte
			if _, err := io.ReadFull(c.reader, x[:]); err != nil {
				return nil, err
			}
			size = uint64(binary.BigEndian.Uint16(x[:]))
		}
		if size == 127 {
			var x [8]byte
			if _, err := io.ReadFull(c.reader, x[:]); err != nil {
				return nil, err
			}
			size = binary.BigEndian.Uint64(x[:])
			if size>>63 != 0 {
				return nil, ErrProtocol
			}
		}
		if size > MaxMessageSize || size > uint64(MaxMessageSize-len(buf)) {
			return nil, ErrProtocol
		}
		if opcode >= 8 && (!fin || size > 125) {
			return nil, ErrProtocol
		}
		b := make([]byte, int(size))
		if _, err := io.ReadFull(c.reader, b); err != nil {
			return nil, err
		}
		switch opcode {
		case 8:
			return nil, io.EOF
		case 9:
			if err := c.writeFrame(ctx, 10, b); err != nil {
				return nil, err
			}
			continue
		case 10:
			continue
		case 1:
			if fragmented {
				return nil, ErrProtocol
			}
			if fin {
				return b, nil
			}
			buf = append(buf, b...)
			fragmented = true
		case 0:
			if !fragmented {
				return nil, ErrProtocol
			}
			buf = append(buf, b...)
			if fin {
				return buf, nil
			}
		default:
			return nil, fmt.Errorf("%w: unexpected opcode %s", ErrProtocol, strconv.Itoa(int(opcode)))
		}
	}
}

func privateIP(ip netip.Addr) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	return ip.Is4() && netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}
