package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Item struct {
	FileID           string `json:"file_id,omitempty"`
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	RelativePath     string `json:"path"`
	SizeBytes        int64  `json:"size_bytes"`
	ModifiedUnixNano int64  `json:"modified_unix_nano"`
}

type Result struct {
	Root       Selector `json:"root"`
	Path       string   `json:"path"`
	Items      []Item   `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

func item(base, name string, info os.FileInfo) Item {
	kind := "file"
	if info.IsDir() {
		kind = "folder"
	}
	rel := filepath.ToSlash(filepath.Join(base, name))
	return Item{Name: name, Kind: kind, RelativePath: rel, SizeBytes: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano()}
}

// List is the first page. ListPage supports offset cursors for reasonably
// sized directories (20k entries). Directory mutations invalidate cursors.
func (c *Catalog) List(s Selector, path string, limit int) (Result, error) {
	return c.ListPage(s, path, limit, "")
}
func (c *Catalog) ListPage(s Selector, path string, limit int, cursor string) (Result, error) {
	if limit < 1 || limit > 1000 {
		return Result{}, fmt.Errorf("INVALID_LIMIT")
	}
	offset := 0
	if cursor != "" {
		v, err := strconv.Atoi(cursor)
		if err != nil || v < 0 || v > 20000 {
			return Result{}, fmt.Errorf("INVALID_CURSOR")
		}
		offset = v
	}
	location, err := c.Resolve(s, path, false)
	if err != nil {
		return Result{}, err
	}
	entries, err := os.ReadDir(location)
	if err != nil {
		return Result{}, err
	}
	if len(entries) > 20000 {
		return Result{}, fmt.Errorf("DIRECTORY_TOO_LARGE")
	}
	out := Result{Root: s, Path: path, Items: make([]Item, 0, limit)}
	// os.ReadDir returns lexical names; only visible regular files/directories
	// contribute to paging offsets. Symlinks are intentionally excluded.
	all := make([]Item, 0, len(entries))
	for _, entry := range entries {
        // Hide incomplete KAN-81 uploads from browsing and media source IDs.
        if strings.HasPrefix(entry.Name(), ".hcss-upload-") { continue }
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := entry.Info()
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
			continue
		}
		all = append(all, item(path, entry.Name(), info))
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := strings.ToLower(all[i].Name), strings.ToLower(all[j].Name)
		if a == b {
			return all[i].Name < all[j].Name
		}
		return a < b
	})
	if offset > len(all) {
		return Result{}, fmt.Errorf("INVALID_CURSOR")
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	out.Items = append(out.Items, all[offset:end]...)
	if end < len(all) {
		out.NextCursor = strconv.Itoa(end)
	}
	return out, nil
}

func (c *Catalog) Stat(s Selector, path string) (Item, error) {
	location, err := c.Resolve(s, path, false)
	if err != nil {
		return Item{}, err
	}
	st, err := os.Stat(location)
	if err != nil {
		return Item{}, err
	}
	if !st.IsDir() && !st.Mode().IsRegular() {
		return Item{}, ErrPathNotAllowed
	}
	name := filepath.Base(location)
	if path == "" {
		name = s.ID
	}
	return item(filepath.Dir(path), name, st), nil
}
