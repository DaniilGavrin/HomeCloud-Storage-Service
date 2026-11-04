package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/catalog"
	"github.com/DaniilGavrin/HomeCloud-Storage-Service/internal/inventory"
	"os"
	"path/filepath"
	"testing"
)

type fakeInspector struct{}

func (fakeInspector) List(context.Context) ([]inventory.Volume, error) {
	return []inventory.Volume{{ID: "v1", MountPath: "/", TotalBytes: 100, Mounted: true}}, nil
}
func TestPerformRootSelection(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0600)
	c, _ := catalog.New([]catalog.Root{{ID: "dir", Kind: catalog.Folder, Path: dir, Permission: catalog.Permission{List: true, Read: true}}})
	a := Control{Catalog: c, Inspector: fakeInspector{}}
	roots, err := a.perform(context.Background(), "storage.agent.roots.list", json.RawMessage(`{}`))
	if err != nil || len(roots.([]catalog.PublicRoot)) != 1 {
		t.Fatal(err, roots)
	}
	vol, err := a.perform(context.Background(), "storage.agent.volumes.list", json.RawMessage(`{}`))
	if err != nil || len(vol.([]inventory.Volume)) != 1 {
		t.Fatal(err, vol)
	}
	item, err := a.perform(context.Background(), "storage.fs.stat", json.RawMessage(`{"root":{"kind":"folder","id":"dir"},"path":"x.txt"}`))
	if err != nil || item.(catalog.Item).Name != "x.txt" {
		t.Fatal(err, item)
	}
	_, err = a.perform(context.Background(), "storage.fs.stat", json.RawMessage(`{"root":{"kind":"disk","id":"dir"},"path":"x.txt"}`))
	if !errors.Is(err, catalog.ErrRootKindMismatch) {
		t.Fatal(err)
	}
	_, err = a.perform(context.Background(), "storage.fs.stat", json.RawMessage(`{"root":{"kind":"folder","id":"dir"},"path":"../etc/passwd"}`))
	if !errors.Is(err, catalog.ErrPathNotAllowed) {
		t.Fatal(err)
	}
	_, err = a.perform(context.Background(), "storage.fs.list", json.RawMessage(`{"root":{"kind":"folder","id":"dir"},"path":"","limit":10}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.perform(context.Background(), "storage.agent.roots.list", json.RawMessage(`{"bad":true}`))
	if err == nil {
		t.Fatal("unknown field accepted")
	}
}
