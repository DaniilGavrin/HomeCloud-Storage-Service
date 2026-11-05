package sourcews

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/netip"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"github.com/coder/websocket"
)

func fixture(t *testing.T) (*Service, catalog.Selector, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "music"), 0700); err != nil { t.Fatal(err) }
	path := filepath.Join(root, "music", "song.mp3")
	if err := os.WriteFile(path, []byte("actual AUDIO contents 0123456789"), 0600); err != nil { t.Fatal(err) }
	c, err := catalog.New([]catalog.Root{{
		ID:"music", Kind:catalog.Folder, Path:root,
		Permission:catalog.Permission{List:true, Read:true},
	}})
	if err != nil { t.Fatal(err) }
	return &Service{NodeID:"storage-1", Catalog:c, AllowedPeers:[]netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		catalog.Selector{Kind:catalog.Folder, ID:"music"}, root
}

func TestStableFileIDAndTraversalRejected(t *testing.T) {
	sel := catalog.Selector{Kind:catalog.Folder, ID:"music"}
	id, err := FileID(sel, "music/song.mp3")
	if err != nil { t.Fatal(err) }
	ref, err := parseID(id)
	if err != nil || ref.Root != sel || ref.Path != "music/song.mp3" { t.Fatal(ref, err) }
	again, err := FileID(sel, "music/song.mp3")
	if err != nil || id != again { t.Fatal("file ID is unstable") }
	for _, p := range []string{"", "../secret", "/absolute", "a/../secret", "a\\b", ".", "C:/foo", "x//y"} {
		if _, err := FileID(sel, p); err == nil { t.Fatalf("accepted path %q", p) }
	}
	for _, id := range []string{"", "hc1.", "hc1.bad.", "x1.YWJj", strings.Repeat("A", MaxFileIDLen+1)} {
		if _, err := parseID(id); err == nil { t.Fatalf("accepted id %q", id[:min(len(id), 50)]) }
	}
}

func TestHCS1StatRangeAndVersion(t *testing.T) {
	s, root, _ := fixture(t)
	id, err := FileID(root, "music/song.mp3")
	if err != nil { t.Fatal(err) }
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil { t.Fatal(err) }
	defer ws.CloseNow()
	send := func(v any) {
		t.Helper()
		payload, err := json.Marshal(v)
		if err != nil { t.Fatal(err) }
		if err := ws.Write(ctx, websocket.MessageText, payload); err != nil { t.Fatal(err) }
	}
	read := func() map[string]json.RawMessage {
		t.Helper()
		kind, data, err := ws.Read(ctx)
		if err != nil || kind != websocket.MessageText { t.Fatalf("text read: %v kind=%v", err,kind) }
		var result map[string]json.RawMessage
		if err := json.Unmarshal(data, &result); err != nil { t.Fatal(err) }
		return result
	}
	send(map[string]any{"v":1,"type":"source.stat","id":"stat","file_id":id})
	m := read()
	if string(m["type"]) != `"source.metadata"` { t.Fatal(m) }
	var meta Metadata
	if err := json.Unmarshal(m["metadata"], &meta); err != nil { t.Fatal(err) }
	if meta.FileID != id || meta.OwnerNodeID != "storage-1" || !meta.RangeReadable ||
		meta.ContentType != "audio/mpeg" || meta.SizeBytes != int64(len("actual AUDIO contents 0123456789")) ||
		meta.Local.SharedRootID != "folder:music" { t.Fatalf("bad metadata %+v", meta) }

	for _, tc := range []struct { offset, size int64; want string; eof bool }{
		{0,6,"actual",false}, {7,5,"AUDIO",false},
		{meta.SizeBytes-4,20,"6789",true},
		{meta.SizeBytes,5,"",true},
	} {
		send(map[string]any{"v":1,"type":"source.read","id":"r","file_id":id,"version":meta.Version,"offset":tc.offset,"length":tc.size})
		reply := read()
		if string(reply["type"]) != `"source.range"` { t.Fatal(reply) }
		var head struct{
			Offset int64 `json:"offset"`
			Length int64 `json:"length"`
			EOF bool `json:"eof"`
			SHA string `json:"checksum_sha256"`
		}
		if err := json.Unmarshal(mustJSON(reply), &head); err != nil { t.Fatal(err) }
		kind, bytesOut, err := ws.Read(ctx)
		if err != nil || kind != websocket.MessageBinary || string(bytesOut) != tc.want || head.Length != int64(len(tc.want)) ||
			head.EOF != tc.eof || head.Offset != tc.offset { t.Fatalf("range %+v kind=%v data=%q err=%v", head,kind,bytesOut,err) }
		h := sha256.Sum256(bytesOut)
		if head.SHA != hex.EncodeToString(h[:]) { t.Fatal("checksum mismatch") }
	}
	send(map[string]any{"v":1,"type":"source.read","id":"stale","file_id":id,"version":"older","offset":0,"length":20})
	if string(read()["code"]) != `"SOURCE_VERSION_CHANGED"` { t.Fatal("stale version should be rejected") }
	send(map[string]any{"v":1,"type":"source.read","id":"huge","file_id":id,"version":meta.Version,"offset":0,"length":MaxRangeBytes+1})
	if string(read()["code"]) != `"INVALID_RANGE"` { t.Fatal("oversize range should be rejected") }
}

func mustJSON(v map[string]json.RawMessage) []byte {
	b,_ := json.Marshal(v)
	return b
}

func TestServiceRejectsNonallowlistedPeer(t *testing.T) {
	s, _, _ := fixture(t)
	s.AllowedPeers = []netip.Addr{netip.MustParseAddr("192.168.1.2")}
	srv := httptest.NewServer(s)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err == nil || response == nil || response.StatusCode != 403 { t.Fatalf("unexpected allowed peer: %v %v",err,response) }
}

func TestServiceNoDirectoryNoSymlinkEscape(t *testing.T) {
	s, root, dir := fixture(t)
	dirID, err := FileID(root, "music")
	if err != nil { t.Fatal(err) }
	if _, err := s.stat(dirID); err == nil { t.Fatal("directory allowed as media file") }
	outside := t.TempDir()
	path := filepath.Join(outside, "secret.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, 8), 0600); err != nil { t.Fatal(err) }
	if err := os.Symlink(path, filepath.Join(dir, "escape.bin")); err != nil { t.Skip(err) }
	id, err := FileID(root, "escape.bin")
	if err != nil { t.Fatal(err) }
	if _, err := s.stat(id); err == nil { t.Fatal("symlink escape accepted") }
}

func TestSourceVersionChangesWhenFileModified(t *testing.T) {
	s, root, dir := fixture(t)
	id, _ := FileID(root, "music/song.mp3")
	old, err := s.stat(id)
	if err != nil { t.Fatal(err) }
	if err := os.WriteFile(filepath.Join(dir,"music","song.mp3"), []byte("different size"), 0600); err != nil { t.Fatal(err) }
	newMeta, err := s.stat(id)
	if err != nil { t.Fatal(err) }
	if old.Version == newMeta.Version { t.Fatal("unchanged version after edit") }
}

func TestPrivatePeerInputValidation(t *testing.T) {
	if _, err := ParsePeerIPs([]string{"0.0.0.0"}); err == nil { t.Fatal("wildcard") }
	if _, err := ParsePeerIPs([]string{"8.8.8.8"}); err == nil { t.Fatal("public IP") }
	if ips, err := ParsePeerIPs([]string{"127.0.0.1", "100.80.1.2", "192.168.0.1"}); err != nil || len(ips) != 3 { t.Fatal(ips,err) }
}
