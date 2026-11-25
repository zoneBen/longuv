//go:build linux

package pty

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPTYRoundTrip(t *testing.T) {
	pair, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer pair.Master.Close()
	if err := ConfigureSlave(pair.SlavePath, 0o666, -1); err != nil {
		t.Fatalf("ConfigureSlave: %v", err)
	}
	slave, err := os.OpenFile(pair.SlavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open slave: %v", err)
	}
	defer slave.Close()

	frame := []byte{0xFF, 0x04, 0x01, 0x50, 0x00, 0x0A, 0x64, 0x3E}
	if _, err := slave.Write(frame); err != nil {
		t.Fatalf("write slave: %v", err)
	}
	got := make([]byte, len(frame))
	if err := readFullTimeout(pair.Master, got); err != nil {
		t.Fatalf("read master: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("master got % X", got)
	}

	reply := []byte{0x01, 0x04, 0x02, 0x00, 0x01, 0xAA, 0xBB}
	if _, err := pair.Master.Write(reply); err != nil {
		t.Fatalf("write master: %v", err)
	}
	got2 := make([]byte, len(reply))
	if err := readFullTimeout(slave, got2); err != nil {
		t.Fatalf("read slave: %v", err)
	}
	if !bytes.Equal(got2, reply) {
		t.Fatalf("slave got % X", got2)
	}
}

func TestPointLinkSafety(t *testing.T) {
	pair, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer pair.Master.Close()
	if err := ConfigureSlave(pair.SlavePath, 0o666, -1); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "com1")
	if err := PointLink(link, pair.SlavePath, false); err != nil {
		t.Fatalf("PointLink: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil || got != pair.SlavePath {
		t.Fatalf("readlink = %q, %v", got, err)
	}
	regular := filepath.Join(dir, "real-device")
	if err := os.WriteFile(regular, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PointLink(regular, pair.SlavePath, false); err == nil {
		t.Fatal("PointLink must refuse to overwrite a regular file")
	}
	if err := PointLink(regular, pair.SlavePath, true); err != nil {
		t.Fatalf("PointLink with replaceNonSymlink: %v", err)
	}
	backups, _ := filepath.Glob(regular + ".bak.*")
	if len(backups) != 1 {
		t.Fatalf("expected one backup, got %v", backups)
	}
	if got, _ := os.Readlink(regular); got != pair.SlavePath {
		t.Fatalf("replaced link = %q", got)
	}
	RemoveLinkIfTarget(link, pair.SlavePath)
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("link not removed: %v", err)
	}
}

type readResult struct {
	n   int
	err error
}

func readFullTimeout(f *os.File, buf []byte) error {
	ch := make(chan readResult, 1)
	go func() {
		n, err := f.Read(buf)
		ch <- readResult{n: n, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if r.n != len(buf) {
			return os.ErrDeadlineExceeded
		}
		return nil
	case <-time.After(2 * time.Second):
		return os.ErrDeadlineExceeded
	}
}
