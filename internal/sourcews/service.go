package sourcews

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"github.com/coder/websocket"
)

type Service struct {
	NodeID       string
	Catalog      *catalog.Catalog
	// Only explicitly listed private LAN / Tailscale IPs may make remote reads.
	// Loopback is allowed only if it is also listed. Never trust forwarded headers.
	AllowedPeers []netip.Addr
}

type request struct {
	V       int    `json:"v"`
	Type    string `json:"type"`
	ID      string `json:"id"`
	FileID  string `json:"file_id"`
	Version string `json:"version,omitempty"`
	Offset  int64  `json:"offset,omitempty"`
	Length  int64  `json:"length,omitempty"`
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || s == nil || s.Catalog == nil || s.NodeID == "" {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil { http.Error(w, "forbidden", http.StatusForbidden); return }
	peer, err := netip.ParseAddr(host)
	if err != nil { http.Error(w, "forbidden", http.StatusForbidden); return }
	allowed := false
	for _, ip := range s.AllowedPeers {
		if ip.Unmap() == peer.Unmap() { allowed = true; break }
	}
	if !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// There is deliberately no browser-Origin trust: the IP allowlist protects
	// the private service from arbitrary internet clients. Requests carry no
	// credentials, so an Origin header can never substitute for peer checking.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // native Go Gateway client has no browser Origin
	})
	if err != nil { return }
	defer conn.CloseNow()
	conn.SetReadLimit(8 << 10)
	for {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		typ, payload, err := conn.Read(ctx)
		cancel()
		if err != nil { return }
		var q request
		if typ != websocket.MessageText || json.Unmarshal(payload, &q) != nil ||
			q.V != 1 || q.ID == "" || len(q.ID) > 128 {
			sendError(conn, "", "INVALID_REQUEST")
			return
		}
		switch q.Type {
		case "source.stat":
			meta, err := s.stat(q.FileID)
			if err != nil { sendError(conn, q.ID, classify(err)); continue }
			if !writeJSON(conn, map[string]any{"v":1,"type":"source.metadata","id":q.ID,"metadata":meta}) { return }
		case "source.read":
			if err := s.sendRange(conn, q); err != nil {
				sendError(conn, q.ID, classify(err))
				// No binary payload is sent on failure.
			}
		default:
			sendError(conn, q.ID, "UNSUPPORTED_COMMAND")
		}
	}
}

func sendError(conn *websocket.Conn, id, code string) {
	_ = writeJSON(conn, map[string]any{"v":1,"type":"source.error","id":id,"code":code})
}

func classify(err error) string {
	switch {
	case errors.Is(err, ErrFileID): return "INVALID_FILE_ID"
	case errors.Is(err, ErrRange): return "INVALID_RANGE"
	case errors.Is(err, ErrStale): return "SOURCE_VERSION_CHANGED"
	case errors.Is(err, catalog.ErrRootNotFound), errors.Is(err, catalog.ErrReadDisabled), errors.Is(err, catalog.ErrRootUnavailable):
		return "SOURCE_UNAVAILABLE"
	case errors.Is(err, catalog.ErrPathNotAllowed): return "PATH_NOT_ALLOWED"
	default: return "SOURCE_UNAVAILABLE"
	}
}

func writeJSON(conn *websocket.Conn, payload any) bool {
	raw, err := json.Marshal(payload)
	if err != nil { return false }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, raw) == nil
}

func (s *Service) open(fileID string) (*os.File, Metadata, error) {
	ref, err := parseID(fileID)
	if err != nil { return nil, Metadata{}, err }
	root, err := s.Catalog.Root(ref.Root)
	if err != nil { return nil, Metadata{}, err }
	if !root.Read || !root.List { return nil, Metadata{}, catalog.ErrReadDisabled }
	// Establish root as an OS-enforced namespace. Unlike Resolve+Open, an
	// intermediate symlink swap cannot escape this opened root descriptor.
	anchored, err := os.OpenRoot(root.Path)
	if err != nil { return nil, Metadata{}, catalog.ErrRootUnavailable }
	f, err := anchored.Open(filepath.FromSlash(ref.Path))
	_ = anchored.Close()
	if err != nil { return nil, Metadata{}, fmt.Errorf("%w: %v", catalog.ErrPathNotAllowed, err) }
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, Metadata{}, catalog.ErrPathNotAllowed
	}
	meta := Metadata{
		OwnerNodeID:s.NodeID, FileID:fileID, Version:fileVersion(ref.Root,ref.Path,st),
		SizeBytes:st.Size(), ContentType:mediaType(ref.Path), RangeReadable:true,
		Local:&LocalHint{SharedRootID:string(ref.Root.Kind)+":"+ref.Root.ID,
			RelativePath:ref.Path, ModifiedUnixNano:st.ModTime().UnixNano()},
	}
	return f, meta, nil
}

func (s *Service) stat(fileID string) (Metadata, error) {
	f, meta, err := s.open(fileID)
	if f != nil { _ = f.Close() }
	return meta, err
}

// sendRange buffers at most 4MiB per request and transmits one binary WS
// message after its JSON header. Generation, room ownership, and TV tickets
// remain with Backend/Gateway: Storage Service does NOT accept TV clients.
func (s *Service) sendRange(conn *websocket.Conn, q request) error {
	if q.Offset < 0 || q.Length < 1 || q.Length > MaxRangeBytes || q.Offset > (1<<63-1)-q.Length {
		return ErrRange
	}
	f, meta, err := s.open(q.FileID)
	if err != nil { return err }
	defer f.Close()
	if q.Version == "" || q.Version != meta.Version { return ErrStale }
	if q.Offset > meta.SizeBytes { return ErrRange }
	n := q.Length
	if remaining := meta.SizeBytes-q.Offset; n > remaining { n = remaining }
	// SectionReader prevents reading past the requested bounded range.
	data := make([]byte, int(n))
	if n > 0 {
		if _, err := io.ReadFull(io.NewSectionReader(f, q.Offset, n), data); err != nil {
			return err
		}
	}
	after, err := f.Stat()
	ref, refErr := parseID(q.FileID)
	if err != nil || refErr != nil || fileVersion(ref.Root, ref.Path, after) != meta.Version {
		return ErrStale
	}
	hash := sha256.Sum256(data)
	header := map[string]any{
		"v":1, "type":"source.range", "id":q.ID,
		"offset":q.Offset, "length":len(data), "version":meta.Version,
		"eof": q.Offset+int64(len(data)) >= meta.SizeBytes,
		"checksum_sha256":hex.EncodeToString(hash[:]),
	}
	if !writeJSON(conn, header) { return fmt.Errorf("write header failed") }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageBinary, data)
}

// ParsePeerIPs validates exact peer addresses; no wildcard, CIDR or public IP.
func ParsePeerIPs(values []string) ([]netip.Addr, error) {
	out := make([]netip.Addr, 0, len(values))
	for _, v := range values {
		ip, err := netip.ParseAddr(strings.TrimSpace(v))
		if err != nil || !ip.IsValid() || ip.IsUnspecified() ||
			!(ip.IsPrivate() || ip.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip.Unmap())) {
			return nil, fmt.Errorf("invalid private Gateway IP %q", v)
		}
		out = append(out, ip.Unmap())
	}
	return out, nil
}
