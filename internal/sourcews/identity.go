// Package sourcews implements the private read-only HCS1 WebSocket file source.
// File IDs name a path under a locally approved root; they are routing labels,
// NOT authorization tokens. The service must not be exposed to untrusted peers.
package sourcews

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
)

const (
	MaxRangeBytes = 4 << 20
	MaxFileIDLen  = 2048
)

var (
	ErrFileID = errors.New("INVALID_FILE_ID")
	ErrStale  = errors.New("SOURCE_VERSION_CHANGED")
	ErrRange  = errors.New("INVALID_RANGE")
)

type fileRef struct {
	Root catalog.Selector `json:"root"`
	Path string           `json:"path"`
}

// FileID is deterministic across service restarts. The encoding contains a
// root-scoped relative path, never a host absolute path. It is not a secret.
func FileID(root catalog.Selector, relative string) (string, error) {
	if root.ID == "" || (root.Kind != catalog.Disk && root.Kind != catalog.Folder) || !validRelative(relative) {
		return "", ErrFileID
	}
	b, _ := json.Marshal(fileRef{Root: root, Path: relative})
	id := "hc1." + base64.RawURLEncoding.EncodeToString(b)
	if len(id) > MaxFileIDLen {
		return "", ErrFileID
	}
	return id, nil
}

func parseID(id string) (fileRef, error) {
	if len(id) > MaxFileIDLen || !strings.HasPrefix(id, "hc1.") {
		return fileRef{}, ErrFileID
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(id, "hc1."))
	if err != nil {
		return fileRef{}, ErrFileID
	}
	var ref fileRef
	if err := json.Unmarshal(b, &ref); err != nil || !validRelative(ref.Path) ||
		ref.Root.ID == "" || (ref.Root.Kind != catalog.Disk && ref.Root.Kind != catalog.Folder) {
		return fileRef{}, ErrFileID
	}
	reencoded, err := FileID(ref.Root, ref.Path)
	if err != nil || reencoded != id {
		return fileRef{}, ErrFileID
	}
	return ref, nil
}

func validRelative(p string) bool {
	return p != "" && p != "." && p == filepath.ToSlash(filepath.Clean(filepath.FromSlash(p))) &&
		!strings.ContainsAny(p, "\\\x00:") && !strings.HasPrefix(p, "/") &&
		filepath.IsLocal(filepath.FromSlash(p))
}

type LocalHint struct {
	SharedRootID     string `json:"shared_root_id"`
	RelativePath     string `json:"relative_path"`
	ModifiedUnixNano int64  `json:"modified_unix_nano"`
}

type Metadata struct {
	OwnerNodeID   string     `json:"owner_node_id"`
	FileID        string     `json:"file_id"`
	Version       string     `json:"version"`
	SizeBytes     int64      `json:"size_bytes"`
	ContentType   string     `json:"content_type,omitempty"`
	RangeReadable bool       `json:"range_readable"`
	Local         *LocalHint `json:"local_hint,omitempty"`
}

func fileVersion(root catalog.Selector, p string, st os.FileInfo) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%d", root.Kind, root.ID, p, st.Size(), st.ModTime().UnixNano())
	return hex.EncodeToString(h.Sum(nil))
}

func mediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv": return "video/x-matroska"
	case ".m4a": return "audio/mp4"
	case ".mp3": return "audio/mpeg"
	case ".flac": return "audio/flac"
	case ".wav": return "audio/wav"
	case ".ogg": return "audio/ogg"
	case ".webm": return "video/webm"
	}
	kind := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if kind == "" { return "application/octet-stream" }
	return kind
}
