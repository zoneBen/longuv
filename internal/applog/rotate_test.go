package applog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w, err := NewRotatingWriter(path, 1, 3)
	if err != nil {
		t.Fatalf("NewRotatingWriter: %v", err)
	}
	defer w.Close()
	chunk := bytes.Repeat([]byte("x"), 600*1024)
	if _, err := w.Write(chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(chunk); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(chunk)) {
		t.Fatalf("current size = %d want %d", info.Size(), len(chunk))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
