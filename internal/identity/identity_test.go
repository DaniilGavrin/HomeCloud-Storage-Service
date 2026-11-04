package identity

import (
	"path/filepath"
	"testing"
)

func TestStableLocalID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "node-id")
	a, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 36 {
		t.Fatalf("node id not stable %q vs %q", a, b)
	}
}
