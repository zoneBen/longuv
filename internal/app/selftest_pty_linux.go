//go:build linux

package app

import (
	"bytes"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/zoneBen/longuv/internal/pty"
)

func testPTY() error {
	pair, err := pty.Open()
	if err != nil {
		return err
	}
	defer pair.Master.Close()
	if err := pty.ConfigureSlave(pair.SlavePath, 0o666, -1); err != nil {
		return err
	}
	slave, err := os.OpenFile(pair.SlavePath, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fmt.Errorf("打开 %s: %w", pair.SlavePath, err)
	}
	defer slave.Close()

	frame := []byte{0xFF, 0x04, 0x01, 0x50, 0x00, 0x0A, 0x64, 0x3E}
	if _, err := slave.Write(frame); err != nil {
		return fmt.Errorf("写 slave: %w", err)
	}
	got := make([]byte, len(frame))
	if err := readWithTimeout(pair.Master, got); err != nil {
		return fmt.Errorf("读 master: %w", err)
	}
	if !bytes.Equal(got, frame) {
		return fmt.Errorf("master 内容不一致: %X", got)
	}

	reply := []byte{0x01, 0x04, 0x02, 0x00, 0x01, 0xAA, 0xBB}
	if _, err := pair.Master.Write(reply); err != nil {
		return fmt.Errorf("写 master: %w", err)
	}
	got2 := make([]byte, len(reply))
	if err := readWithTimeout(slave, got2); err != nil {
		return fmt.Errorf("读 slave: %w", err)
	}
	if !bytes.Equal(got2, reply) {
		return fmt.Errorf("slave 内容不一致: %X", got2)
	}
	return nil
}

type readResult struct {
	n   int
	err error
}

func readWithTimeout(f *os.File, buf []byte) error {
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
			return fmt.Errorf("读到 %d/%d 字节", r.n, len(buf))
		}
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("超时")
	}
}
