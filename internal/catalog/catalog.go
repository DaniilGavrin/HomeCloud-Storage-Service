// Package catalog owns the root-scoped filesystem namespace of HC Storage Service.
// Root IDs are local configuration references, not credentials or remote paths.
package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Kind string

const (
	Disk   Kind = "disk"
	Folder Kind = "folder"
)

var (
	ErrBadSelector      = errors.New("INVALID_ROOT_SELECTOR")
	ErrRootNotFound     = errors.New("ROOT_NOT_FOUND")
	ErrRootKindMismatch = errors.New("ROOT_KIND_MISMATCH")
	ErrRootUnavailable  = errors.New("ROOT_UNAVAILABLE")
	ErrPathNotAllowed   = errors.New("PATH_NOT_ALLOWED")
	ErrReadDisabled     = errors.New("ROOT_READ_DISABLED")
)

type Selector struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
}

type Permission struct {
	List   bool `json:"allow_list"`
	Read   bool `json:"allow_read"`
	Write  bool `json:"allow_write"`
	Delete bool `json:"allow_delete"`
}

type Root struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
	Permission
}

// PublicRoot intentionally never includes host filesystem paths.
type PublicRoot struct {
	Selector
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Permission
}

type Catalog struct{ roots map[string]Root }

func New(roots []Root) (*Catalog, error) {
	c := &Catalog{roots: make(map[string]Root, len(roots))}
	for _, r := range roots {
		if r.ID == "" || len(r.ID) > 128 || strings.TrimSpace(r.ID) != r.ID || strings.ContainsAny(r.ID, "\\/\r\n\x00") || (r.Kind != Disk && r.Kind != Folder) {
			return nil, fmt.Errorf("%w: %q", ErrBadSelector, r.ID)
		}
		if !filepath.IsAbs(r.Path) {
			return nil, fmt.Errorf("%w: root path for %s must be absolute", ErrPathNotAllowed, r.ID)
		}
		if _, present := c.roots[r.ID]; present {
			return nil, fmt.Errorf("%w: duplicate root %s", ErrBadSelector, r.ID)
		}
		r.Path = filepath.Clean(r.Path)
		c.roots[r.ID] = r
	}
	return c, nil
}

func (c *Catalog) Root(s Selector) (Root, error) {
	if c == nil || s.ID == "" || (s.Kind != Disk && s.Kind != Folder) {
		return Root{}, ErrBadSelector
	}
	r, ok := c.roots[s.ID]
	if !ok {
		return Root{}, ErrRootNotFound
	}
	if r.Kind != s.Kind {
		return Root{}, ErrRootKindMismatch
	}
	return r, nil
}

func (c *Catalog) Published() []PublicRoot {
	if c == nil {
		return nil
	}
	out := make([]PublicRoot, 0, len(c.roots))
	for _, r := range c.roots {
		st, e := os.Stat(r.Path)
		online := e == nil && st.IsDir()
		name := r.Name
		if name == "" {
			name = r.ID
		}
		out = append(out, PublicRoot{Selector: Selector{Kind: r.Kind, ID: r.ID}, Name: name, Online: online, Permission: r.Permission})
	}
	// Ensure deterministic listing and snapshot order.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// inside guarantees the target stays within the canonical allowed root.
// This is a v0 read-only safety check. KAN-39 production readers will use
// descriptor-based opens to prevent symlink/rename races (TOCTOU).
func inside(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && (rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))))
}

func (c *Catalog) Resolve(s Selector, relative string, needRead bool) (string, error) {
	r, err := c.Root(s)
	if err != nil {
		return "", err
	}
	if !r.List || (needRead && !r.Read) {
		return "", ErrReadDisabled
	}
	if strings.ContainsRune(relative, 0) || filepath.IsAbs(relative) || !filepath.IsLocal(relative) && relative != "" {
		return "", ErrPathNotAllowed
	}
	// Reject a client-supplied Windows-style drive/UNC prefix even on Linux.
	if strings.Contains(relative, "\\") || strings.Contains(relative, ":") {
		return "", ErrPathNotAllowed
	}
	root, err := filepath.EvalSymlinks(r.Path)
	if err != nil {
		return "", ErrRootUnavailable
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return "", ErrRootUnavailable
	}
	file := root
	if relative != "" {
		file = filepath.Join(root, relative)
	}
	target, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", err
	}
	if !inside(root, target) {
		return "", ErrPathNotAllowed
	}
	return target, nil
}
