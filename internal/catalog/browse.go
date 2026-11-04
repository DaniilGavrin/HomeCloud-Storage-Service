package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Item struct {
	Name             string `json:"name"`
	Kind             string `json:"kind"`
	RelativePath     string `json:"path"`
	SizeBytes        int64  `json:"size_bytes"`
	ModifiedUnixNano int64  `json:"modified_unix_nano"`
}

type Result struct {
	Root  Selector `json:"root"`
	Path  string   `json:"path"`
	Items []Item   `json:"items"`
}

func item(base, name string, info os.FileInfo) Item {
	kind := "file"
	if info.IsDir() {
		kind = "folder"
	}
	rel := filepath.ToSlash(filepath.Join(base, name))
	return Item{Name: name, Kind: kind, RelativePath: rel, SizeBytes: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano()}
}

func (c *Catalog) List(s Selector, path string, limit int) (Result, error) {
	if limit < 1 || limit > 1000 {
		return Result{}, fmt.Errorf("INVALID_LIMIT")
	}
	location, err := c.Resolve(s, path, false)
	if err != nil {
		return Result{}, err
	}
	f, err := os.Open(location)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Result{}, err
	}
	if !st.IsDir() {
		return Result{}, fmt.Errorf("NOT_A_DIRECTORY")
	}
	entries, err := f.ReadDir(limit + 1)
	if err != nil {
		return Result{}, err
	}
	if len(entries) > limit {
		return Result{}, fmt.Errorf("RESULT_LIMIT_EXCEEDED: pagination is not implemented yet")
	}
	out := Result{Root: s, Path: path, Items: make([]Item, 0, len(entries))}
	for _, entry := range entries {
		// Refuse symlinks in the v0 listing; later descriptor-based APIs can expose them safely.
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		inf, e := entry.Info()
		if e != nil || (!inf.IsDir() && !inf.Mode().IsRegular()) {
			continue
		}
		out.Items = append(out.Items, item(path, entry.Name(), inf))
	}
	sort.Slice(out.Items, func(i, j int) bool { return strings.ToLower(out.Items[i].Name) < strings.ToLower(out.Items[j].Name) })
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
