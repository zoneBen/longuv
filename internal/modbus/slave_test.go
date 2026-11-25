package modbus_test

import (
	"bytes"
	"testing"

	"github.com/zoneBen/longuv/internal/modbus"
)

func TestAdoptAddressNegotiation(t *testing.T) {
	dev := goldenDevice(t)
	var got uint8
	slave := &modbus.Slave{
		Addr: 1, Bank: dev, BcastReplyOwn: true, AllowAdopt: true,
		OnAdopt: func(u uint8) error { got = u; return nil },
	}
	req := modbus.AppendCRC([]byte{0x01, 0x06, 0x00, 0x01, 0x00, 0x05})
	resp := slave.Handle(req)
	if !bytes.Equal(resp, req) {
		t.Fatalf("FC06 must echo the request: %s", modbus.Hex(resp))
	}
	if got != 5 {
		t.Fatalf("adopt callback got %d", got)
	}
	words, code := dev.ReadHolding(1, 1, 5)
	if code != 0 || words[0] != 5 {
		t.Fatalf("info address with unit 5 = %#v code=%d", words, code)
	}
}

func TestAdoptDisabled(t *testing.T) {
	dev := goldenDevice(t)
	called := false
	slave := &modbus.Slave{
		Addr: 1, Bank: dev, BcastReplyOwn: true, AllowAdopt: false,
		OnAdopt: func(u uint8) error { called = true; return nil },
	}
	req := modbus.AppendCRC([]byte{0x01, 0x06, 0x00, 0x01, 0x00, 0x05})
	if resp := slave.Handle(req); !bytes.Equal(resp, req) {
		t.Fatalf("FC06 must echo the request: %s", modbus.Hex(resp))
	}
	if called {
		t.Fatal("adopt callback must not run when disabled")
	}
}

func TestBroadcastWriteIsSilent(t *testing.T) {
	dev := goldenDevice(t)
	slave := &modbus.Slave{Addr: 1, Bank: dev, BcastReplyOwn: true}

	req := modbus.AppendCRC([]byte{0xFF, 0x06, 0x01, 0x00, 0x00, 0x10})
	if resp := slave.Handle(req); resp != nil {
		t.Fatalf("0xFF broadcast write must not reply: %s", modbus.Hex(resp))
	}
	words, _ := dev.ReadHolding(0x0100, 1, 1)
	if words[0] != 0x0010 {
		t.Fatalf("broadcast LED write not applied: %#x", words[0])
	}

	req = modbus.AppendCRC([]byte{0x00, 0x06, 0x01, 0x01, 0x00, 0x20})
	if resp := slave.Handle(req); resp != nil {
		t.Fatalf("standard broadcast write must not reply: %s", modbus.Hex(resp))
	}
	words, _ = dev.ReadHolding(0x0101, 1, 1)
	if words[0] != 0x0020 {
		t.Fatalf("standard broadcast LED write not applied: %#x", words[0])
	}
}

func TestWrongAddressIgnored(t *testing.T) {
	dev := goldenDevice(t)
	slave := &modbus.Slave{Addr: 1, Bank: dev, BcastReplyOwn: true}
	req := modbus.AppendCRC([]byte{0x09, 0x03, 0x00, 0x00, 0x00, 0x06})
	if resp := slave.Handle(req); resp != nil {
		t.Fatalf("wrong address should be ignored: %s", modbus.Hex(resp))
	}
}

func TestBadCRCIgnored(t *testing.T) {
	dev := goldenDevice(t)
	slave := &modbus.Slave{Addr: 1, Bank: dev, BcastReplyOwn: true}
	req := modbus.AppendCRC([]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x06})
	req[len(req)-1] ^= 0xFF
	if resp := slave.Handle(req); resp != nil {
		t.Fatalf("bad CRC should be ignored: %s", modbus.Hex(resp))
	}
}
