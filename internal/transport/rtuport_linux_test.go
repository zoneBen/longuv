//go:build linux

package transport_test

import (
	"bytes"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/modbus"
	"github.com/zoneBen/longuv/internal/transport"
)

func readFrame(t *testing.T, f *os.File, n int) []byte {
	t.Helper()
	type result struct {
		buf []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, n)
		_, err := io.ReadFull(f, buf)
		ch <- result{buf: buf, err: err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read %d bytes: %v", n, r.err)
		}
		return r.buf
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout reading %d bytes", n)
		return nil
	}
}

func request(t *testing.T, f *os.File, pduHex string, replyLen int) []byte {
	t.Helper()
	pdu, err := hexDecode(pduHex)
	if err != nil {
		t.Fatal(err)
	}
	frame := modbus.AppendCRC(pdu)
	if _, err := f.Write(frame); err != nil {
		t.Fatalf("write %s: %v", pduHex, err)
	}
	return readFrame(t, f, replyLen)
}

func hexDecode(s string) ([]byte, error) { return hex.DecodeString(s) }

func TestRTUPortEndToEndSharedDevice(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	disabled := false
	cfg.Devices[0].Climate.Enabled = &disabled
	cfg.Devices[0].Fixed = config.FixedTH{Temp: 26.5, Humi: 48.0}
	cfg.Devices[0].ESN = "iADU-SIM-00001"
	for i := range cfg.Ports {
		cfg.Ports[i].ResponseDelayMS = 0
		cfg.Ports[i].TxSilenceMS = 0
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := hub.New(cfg, framelog.New(64), logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	ring := h.Ring()
	ports := make([]*transport.RTUPort, 0, len(cfg.Ports))
	for _, pc := range cfg.Ports {
		port := transport.NewRTUPort(pc, h, ring, logger)
		if err := port.Start(); err != nil {
			t.Fatalf("start %s: %v", pc.Name, err)
		}
		ports = append(ports, port)
	}
	defer func() {
		for _, p := range ports {
			p.Stop()
		}
	}()

	links := make([]string, len(cfg.Ports))
	for i, pc := range cfg.Ports {
		links[i] = pc.Link
	}
	com1, err := os.OpenFile(links[0], os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open COM1 link: %v", err)
	}
	defer com1.Close()
	com2, err := os.OpenFile(links[1], os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open COM2 link: %v", err)
	}
	defer com2.Close()

	// Broadcast discovery must yield the 20-byte ESN answer with a valid CRC.
	resp := request(t, com1, "FF040150000A", 25)
	if !modbus.CRCOK(resp) || resp[0] != 0x01 || resp[1] != 0x04 || resp[2] != 20 {
		t.Fatalf("discovery response = %s", modbus.Hex(resp))
	}
	if !bytes.HasPrefix(resp[3:], []byte("iADU-SIM-00001")) {
		t.Fatalf("ESN = %q", resp[3:])
	}

	// Fixed climate values are visible through the RTU path.
	resp = request(t, com1, "010401000003", 11)
	if !modbus.CRCOK(resp) || resp[3] != 0x85 || resp[4] != 0x60 || resp[5] != 0x85 || resp[6] != 0x60 {
		t.Fatalf("TH response = %s", modbus.Hex(resp))
	}

	// Write U1 LED through COM2, then observe it through COM1: both bindings
	// share the same device state.
	if resp = request(t, com2, "010601000055", 8); !modbus.CRCOK(resp) || resp[4] != 0x00 || resp[5] != 0x55 {
		t.Fatalf("COM2 LED write = %s", modbus.Hex(resp))
	}
	if resp = request(t, com1, "010301000001", 7); !modbus.CRCOK(resp) || resp[3] != 0x00 || resp[4] != 0x55 {
		t.Fatalf("COM1 LED readback = %s", modbus.Hex(resp))
	}

	// A collector reconnect must work while the master stays open.
	usb := request(t, com2, "010400000004", 13) // U1 tag, 8 bytes
	if !modbus.CRCOK(usb) {
		t.Fatalf("COM2 tag read = %s", modbus.Hex(usb))
	}
	_ = com1.Close()
	com1, err = os.OpenFile(links[0], os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("reopen COM1 link: %v", err)
	}
	defer com1.Close()
	resp = request(t, com1, "010300000006", 17) // info registers
	if !modbus.CRCOK(resp) || len(resp) != 17 {
		t.Fatalf("reopened info response = %s", modbus.Hex(resp))
	}

	infos := h.PortInfos()
	if len(infos) != 2 {
		t.Fatalf("port infos = %d", len(infos))
	}
	for _, info := range infos {
		if info.RX == 0 || info.TX == 0 || info.SlavePath == "" {
			t.Fatalf("port stats = %+v", info)
		}
	}
	if h.Ring().Len() == 0 {
		t.Fatal("frame log is empty")
	}

	// Stop removes the stable links but leaves the process alive.
	for _, p := range ports {
		p.Stop()
	}
	for _, link := range links {
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("stable link %s still exists: %v", link, err)
		}
	}
}

func TestRTUPortStartRejectsNonPTY(t *testing.T) {
	port := transport.NewRTUPort(config.Port{Name: "SER1", Transport: "serial"}, nil, nil, nil)
	if err := port.Start(); err == nil {
		port.Stop()
		t.Fatal("non-pty transport should be rejected")
	}
}
