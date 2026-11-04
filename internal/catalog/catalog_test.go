package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskAndFolderSelectorsAndIsolation(t *testing.T) {
	disk := t.TempDir()
	photos := filepath.Join(disk, "Photos")
	if err := os.Mkdir(photos, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disk, "disk.txt"), []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(photos, "photo.txt"), []byte("photo"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New([]Root{{ID: "drive", Kind: Disk, Path: disk, Permission: Permission{List: true, Read: true}}, {ID: "photos", Kind: Folder, Path: photos, Permission: Permission{List: true, Read: true}}})
	if err != nil {
		t.Fatal(err)
	}
	diskList, err := c.List(Selector{Disk, "drive"}, "", 10)
	if err != nil || len(diskList.Items) != 2 {
		t.Fatalf("disk %v %+v", err, diskList)
	}
	folderList, err := c.List(Selector{Folder, "photos"}, "", 10)
	if err != nil || len(folderList.Items) != 1 || folderList.Items[0].Name != "photo.txt" {
		t.Fatalf("folder %v %+v", err, folderList)
	}
	if _, err := c.List(Selector{Disk, "photos"}, "", 10); !errors.Is(err, ErrRootKindMismatch) {
		t.Fatal("kind mismatch", err)
	}
	if _, err := c.List(Selector{Folder, "photos"}, "../disk.txt", 10); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatal("escape", err)
	}
	if _, err := c.Resolve(Selector{Folder, "photos"}, "/etc/passwd", true); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatal("absolute", err)
	}
	if _, err := c.Resolve(Selector{Folder, "photos"}, "C:\\Windows", true); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatal("windows path", err)
	}
	roots := c.Published()
	if len(roots) != 2 || roots[0].ID != "drive" || roots[1].ID != "photos" {
		t.Fatalf("published %+v", roots)
	}
}

func TestSymlinkEscapesRejected(t *testing.T) {
	parent := t.TempDir()
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "file"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(parent, "link")); err != nil {
		t.Skip(err)
	}
	c, err := New([]Root{{ID: "f", Kind: Folder, Path: parent, Permission: Permission{List: true, Read: true}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Resolve(Selector{Folder, "f"}, "link/file", true)
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Fatal(err)
	}
}

func TestDefaultDenyAndConfig(t *testing.T) {
	c, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Published()) != 0 {
		t.Fatal("roots should be empty")
	}
	if _, err := New([]Root{{ID: "bad", Kind: "drive", Path: "/"}}); !errors.Is(err, ErrBadSelector) {
		t.Fatal(err)
	}
	if _, err := New([]Root{{ID: "relative", Kind: Folder, Path: "rel"}}); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatal(err)
	}
	c, err = New([]Root{{ID: "r", Kind: Folder, Path: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resolve(Selector{Folder, "r"}, "", false); !errors.Is(err, ErrReadDisabled) {
		t.Fatal(err)
	}
}
