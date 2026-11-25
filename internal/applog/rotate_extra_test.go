package applog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNewRotatingWriterErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewRotatingWriter(dir, 1, 1); err == nil {
		t.Fatal("opening a directory as a log file should fail")
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRotatingWriter(filepath.Join(blocker, "sub", "app.log"), 1, 1); err == nil {
		t.Fatal("creating a log directory under a file should fail")
	}
}

func TestDefaultsAndCloseSemantics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "defaults.log")
	w, err := NewRotatingWriter(path, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if w.maxBytes != 20<<20 || w.maxBackups != 5 {
		t.Fatalf("defaults = %d/%d", w.maxBytes, w.maxBackups)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("log file not created: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := w.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close = %v", err)
	}
}

func TestRotationKeepsConfiguredBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w, err := NewRotatingWriter(path, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	chunk := bytes.Repeat([]byte("y"), 600*1024)
	for i := 0; i < 4; i++ {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	for _, name := range []string{path, path + ".1", path + ".2"} {
		if _, err := os.Stat(name); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: %v", err)
	}
}
