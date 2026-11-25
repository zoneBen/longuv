package modbus_test

import (
	"bytes"
	"testing"

	"github.com/zoneBen/longuv/internal/modbus"
)

type fakeBank struct {
	input    []uint16
	holding  []uint16
	coils    []bool
	discrete []bool

	readCode  uint8
	writeCode uint8

	panicRead  bool
	panicWrite bool

	lastStart uint16
	lastCount uint16
	lastUnit  uint8

	wroteHolding  map[uint16]uint16
	wroteMany     []uint16
	wroteCoil     map[uint16]bool
	wroteCoils    []bool
	writeManySeen bool
}

func (f *fakeBank) ReadInput(start, count uint16, unit uint8) ([]uint16, uint8) {
	if f.panicRead {
		panic("read input boom")
	}
	f.lastStart, f.lastCount, f.lastUnit = start, count, unit
	if f.readCode != 0 {
		return nil, f.readCode
	}
	return f.input, 0
}

func (f *fakeBank) ReadHolding(start, count uint16, unit uint8) ([]uint16, uint8) {
	if f.panicRead {
		panic("read holding boom")
	}
	f.lastStart, f.lastCount, f.lastUnit = start, count, unit
	if f.readCode != 0 {
		return nil, f.readCode
	}
	return f.holding, 0
}

func (f *fakeBank) ReadCoils(start, count uint16) ([]bool, uint8) {
	if f.panicRead {
		panic("read coils boom")
	}
	f.lastStart, f.lastCount = start, count
	if f.readCode != 0 {
		return nil, f.readCode
	}
	return f.coils, 0
}

func (f *fakeBank) ReadDiscrete(start, count uint16) ([]bool, uint8) {
	if f.panicRead {
		panic("read discrete boom")
	}
	f.lastStart, f.lastCount = start, count
	if f.readCode != 0 {
		return nil, f.readCode
	}
	return f.discrete, 0
}

func (f *fakeBank) WriteHolding(addr, value uint16, _ uint8) uint8 {
	if f.panicWrite {
		panic("write holding boom")
	}
	if f.wroteHolding == nil {
		f.wroteHolding = make(map[uint16]uint16)
	}
	f.wroteHolding[addr] = value
	return f.writeCode
}

func (f *fakeBank) WriteHoldingMany(start uint16, values []uint16, _ uint8) uint8 {
	if f.panicWrite {
		panic("write holding many boom")
	}
	f.writeManySeen = true
	f.wroteMany = append([]uint16(nil), values...)
	return f.writeCode
}

func (f *fakeBank) WriteCoil(addr uint16, on bool) uint8 {
	if f.panicWrite {
		panic("write coil boom")
	}
	if f.wroteCoil == nil {
		f.wroteCoil = make(map[uint16]bool)
	}
	f.wroteCoil[addr] = on
	return f.writeCode
}

func (f *fakeBank) WriteCoilsMany(_ uint16, values []bool) uint8 {
	if f.panicWrite {
		panic("write coils many boom")
	}
	f.writeManySeen = true
	f.wroteCoils = append([]bool(nil), values...)
	return f.writeCode
}

func frame(addr, fc byte, body ...byte) []byte {
	return modbus.AppendCRC(append([]byte{addr, fc}, body...))
}

func response(addr, fc byte, body ...byte) []byte {
	return modbus.AppendCRC(append([]byte{addr, fc}, body...))
}

func exception(addr, fc, code byte) []byte {
	return modbus.AppendCRC([]byte{addr, fc | 0x80, code})
}

func newSlave(bank *fakeBank) *modbus.Slave {
	return &modbus.Slave{Addr: 1, Bank: bank, BcastReplyOwn: true}
}

func TestSlaveReadPDUs(t *testing.T) {
	bank := &fakeBank{
		holding: []uint16{0x1122, 0x3344},
		input:   []uint16{0x5566, 0x7788},
		coils:   []bool{true, false, true},
		discrete: []bool{
			true, false, true,
		},
	}
	s := newSlave(bank)

	cases := []struct {
		name string
		req  []byte
		want []byte
	}{
		{"FC03", frame(1, 0x03, 0x00, 0x00, 0x00, 0x02), response(1, 0x03, 0x04, 0x11, 0x22, 0x33, 0x44)},
		{"FC04", frame(1, 0x04, 0x00, 0x00, 0x00, 0x02), response(1, 0x04, 0x04, 0x55, 0x66, 0x77, 0x88)},
		{"FC01", frame(1, 0x01, 0x00, 0x00, 0x00, 0x03), response(1, 0x01, 0x01, 0x05)},
		{"FC02", frame(1, 0x02, 0x00, 0x00, 0x00, 0x03), response(1, 0x02, 0x01, 0x05)},
	}
	for _, tc := range cases {
		got := s.Handle(tc.req)
		if !bytes.Equal(got, tc.want) {
			t.Fatalf("%s = %s want %s", tc.name, modbus.Hex(got), modbus.Hex(tc.want))
		}
	}
	if bank.lastStart != 0 || bank.lastCount != 3 || bank.lastUnit != 1 {
		t.Fatalf("bank arguments = %d/%d/%d", bank.lastStart, bank.lastCount, bank.lastUnit)
	}
}

func TestSlaveBroadcastReadAddress(t *testing.T) {
	bank := &fakeBank{holding: []uint16{1}}
	s := newSlave(bank)
	req := frame(0xFF, 0x03, 0x00, 0x00, 0x00, 0x01)
	got := s.Handle(req)
	want := response(1, 0x03, 0x02, 0x00, 0x01)
	if !bytes.Equal(got, want) {
		t.Fatalf("own reply = %s want %s", modbus.Hex(got), modbus.Hex(want))
	}
	s.BcastReplyOwn = false
	got = s.Handle(req)
	want = response(0xFF, 0x03, 0x02, 0x00, 0x01)
	if !bytes.Equal(got, want) {
		t.Fatalf("ff reply = %s want %s", modbus.Hex(got), modbus.Hex(want))
	}
	if got := s.Handle(frame(0x00, 0x03, 0x00, 0x00, 0x00, 0x01)); got != nil {
		t.Fatalf("standard broadcast read must be silent: %s", modbus.Hex(got))
	}
}

func TestSlaveReadExceptions(t *testing.T) {
	s := newSlave(&fakeBank{})
	cases := []struct {
		name string
		req  []byte
		want []byte
	}{
		{"short FC01", frame(1, 0x01, 0x00, 0x00, 0x00), exception(1, 0x01, 0x03)},
		{"short FC03", frame(1, 0x03, 0x00, 0x00, 0x00), exception(1, 0x03, 0x03)},
		{"FC01 zero", frame(1, 0x01, 0x00, 0x00, 0x00, 0x00), exception(1, 0x01, 0x02)},
		{"FC01 too many", frame(1, 0x01, 0x00, 0x00, 0x00, 0x7E), exception(1, 0x01, 0x02)},
		{"FC03 zero", frame(1, 0x03, 0x00, 0x00, 0x00, 0x00), exception(1, 0x03, 0x02)},
		{"FC03 too many", frame(1, 0x03, 0x00, 0x00, 0x00, 0x7E), exception(1, 0x03, 0x02)},
		{"FC04 overflow", frame(1, 0x04, 0x5F, 0xFF, 0x00, 0x02), exception(1, 0x04, 0x02)},
		{"unknown FC", frame(1, 0x07), exception(1, 0x07, 0x01)},
	}
	for _, tc := range cases {
		got := s.Handle(tc.req)
		if !bytes.Equal(got, tc.want) {
			t.Fatalf("%s = %s want %s", tc.name, modbus.Hex(got), modbus.Hex(tc.want))
		}
	}
	if got := s.Handle(frame(0xFF, 0x07)); got != nil {
		t.Fatalf("broadcast unknown FC must be silent: %s", modbus.Hex(got))
	}
	if got := s.Handle([]byte{1, 2, 3}); got != nil {
		t.Fatalf("short frame must be ignored: %s", modbus.Hex(got))
	}
}

func TestSlaveBankReadErrors(t *testing.T) {
	for _, fc := range []byte{0x01, 0x02} {
		bank := &fakeBank{readCode: modbus.ExcDeviceFailure}
		s := newSlave(bank)
		got := s.Handle(frame(1, fc, 0x00, 0x00, 0x00, 0x01))
		if !bytes.Equal(got, exception(1, fc, modbus.ExcDeviceFailure)) {
			t.Fatalf("FC%02X bank error = %s", fc, modbus.Hex(got))
		}
	}
	for _, fc := range []byte{0x03, 0x04} {
		bank := &fakeBank{readCode: modbus.ExcIllegalAddress}
		s := newSlave(bank)
		got := s.Handle(frame(1, fc, 0x00, 0x00, 0x00, 0x01))
		if !bytes.Equal(got, exception(1, fc, modbus.ExcIllegalAddress)) {
			t.Fatalf("FC%02X bank error = %s", fc, modbus.Hex(got))
		}
	}
}

func TestSlaveSingleWrites(t *testing.T) {
	bank := &fakeBank{}
	s := newSlave(bank)
	req := frame(1, 0x05, 0x00, 0x10, 0xFF, 0x00)
	if got := s.Handle(req); !bytes.Equal(got, req) {
		t.Fatalf("FC05 echo = %s", modbus.Hex(got))
	}
	if !bank.wroteCoil[0x10] {
		t.Fatal("FC05 not applied")
	}
	req = frame(1, 0x05, 0x00, 0x11, 0x00, 0x00)
	s.Handle(req)
	if bank.wroteCoil[0x11] {
		t.Fatal("FC05 off not applied")
	}
	req = frame(1, 0x06, 0x01, 0x00, 0x00, 0x20)
	if got := s.Handle(req); !bytes.Equal(got, req) {
		t.Fatalf("FC06 echo = %s", modbus.Hex(got))
	}
	if bank.wroteHolding[0x0100] != 0x20 {
		t.Fatalf("FC06 value = %#x", bank.wroteHolding[0x0100])
	}
}

func TestSlaveWriteExceptionsAndBroadcast(t *testing.T) {
	bank := &fakeBank{writeCode: modbus.ExcIllegalAddress}
	s := newSlave(bank)
	if got := s.Handle(frame(1, 0x05, 0x00, 0x10, 0xFF, 0x00)); !bytes.Equal(got, exception(1, 0x05, 0x02)) {
		t.Fatalf("FC05 error = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(1, 0x06, 0x00, 0x10, 0x00, 0x01)); !bytes.Equal(got, exception(1, 0x06, 0x02)) {
		t.Fatalf("FC06 error = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(1, 0x05, 0x00, 0x10, 0xFF)); !bytes.Equal(got, exception(1, 0x05, 0x03)) {
		t.Fatalf("short FC05 = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(1, 0x06, 0x00, 0x10, 0x00)); !bytes.Equal(got, exception(1, 0x06, 0x03)) {
		t.Fatalf("short FC06 = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(0xFF, 0x06, 0x00, 0x10, 0x00, 0x01)); got != nil {
		t.Fatalf("broadcast FC06 error must be silent: %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(0xFF, 0x05, 0x00, 0x10, 0xFF)); got != nil {
		t.Fatalf("broadcast short FC05 must be silent: %s", modbus.Hex(got))
	}
}

func TestSlaveMultiWrites(t *testing.T) {
	bank := &fakeBank{}
	s := newSlave(bank)
	req := frame(1, 0x0F, 0x00, 0x05, 0x00, 0x03, 0x01, 0x05)
	got := s.Handle(req)
	want := response(1, 0x0F, 0x00, 0x05, 0x00, 0x03)
	if !bytes.Equal(got, want) {
		t.Fatalf("FC0F response = %s want %s", modbus.Hex(got), modbus.Hex(want))
	}
	if !bank.writeManySeen || len(bank.wroteCoils) != 3 ||
		!bank.wroteCoils[0] || bank.wroteCoils[1] || !bank.wroteCoils[2] {
		t.Fatalf("FC0F values = %#v", bank.wroteCoils)
	}
	bank.writeManySeen = false
	req = frame(1, 0x10, 0x01, 0x00, 0x00, 0x02, 0x04, 0x11, 0x22, 0x33, 0x44)
	got = s.Handle(req)
	want = response(1, 0x10, 0x01, 0x00, 0x00, 0x02)
	if !bytes.Equal(got, want) {
		t.Fatalf("FC10 response = %s want %s", modbus.Hex(got), modbus.Hex(want))
	}
	if !bank.writeManySeen || len(bank.wroteMany) != 2 || bank.wroteMany[0] != 0x1122 || bank.wroteMany[1] != 0x3344 {
		t.Fatalf("FC10 values = %#v", bank.wroteMany)
	}
}

func TestSlaveMultiWriteExceptions(t *testing.T) {
	s := newSlave(&fakeBank{})
	cases := []struct {
		name string
		req  []byte
		want []byte
	}{
		{"FC0F short", frame(1, 0x0F, 0x00, 0x00, 0x00, 0x01), exception(1, 0x0F, 0x03)},
		{"FC0F zero count", frame(1, 0x0F, 0x00, 0x00, 0x00, 0x00, 0x00), exception(1, 0x0F, 0x03)},
		{"FC0F bad byte count", frame(1, 0x0F, 0x00, 0x00, 0x00, 0x09, 0x01, 0xFF), exception(1, 0x0F, 0x03)},
		{"FC0F overflow", frame(1, 0x0F, 0x5F, 0xFF, 0x00, 0x02, 0x01, 0x03), exception(1, 0x0F, 0x03)},
		{"FC10 short", frame(1, 0x10, 0x00, 0x00, 0x00, 0x01), exception(1, 0x10, 0x03)},
		{"FC10 zero count", frame(1, 0x10, 0x00, 0x00, 0x00, 0x00, 0x00), exception(1, 0x10, 0x03)},
		{"FC10 bad byte count", frame(1, 0x10, 0x00, 0x00, 0x00, 0x02, 0x02, 0x00, 0x01), exception(1, 0x10, 0x03)},
		{"FC10 overflow", frame(1, 0x10, 0x5F, 0xFF, 0x00, 0x02, 0x04, 0x00, 0x01, 0x00, 0x02), exception(1, 0x10, 0x03)},
	}
	for _, tc := range cases {
		if got := s.Handle(tc.req); !bytes.Equal(got, tc.want) {
			t.Fatalf("%s = %s want %s", tc.name, modbus.Hex(got), modbus.Hex(tc.want))
		}
	}
}

func TestSlaveMultiWriteBankErrorsAndBroadcast(t *testing.T) {
	bank := &fakeBank{writeCode: modbus.ExcDeviceFailure}
	s := newSlave(bank)
	if got := s.Handle(frame(1, 0x0F, 0x00, 0x00, 0x00, 0x01, 0x01, 0x01)); !bytes.Equal(got, exception(1, 0x0F, 0x04)) {
		t.Fatalf("FC0F bank error = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(1, 0x10, 0x00, 0x00, 0x00, 0x01, 0x02, 0x00, 0x01)); !bytes.Equal(got, exception(1, 0x10, 0x04)) {
		t.Fatalf("FC10 bank error = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(0xFF, 0x0F, 0x00, 0x00, 0x00, 0x01, 0x01, 0x01)); got != nil {
		t.Fatalf("broadcast FC0F error must be silent: %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(0x00, 0x10, 0x00, 0x00, 0x00, 0x01, 0x02, 0x00, 0x01)); got != nil {
		t.Fatalf("standard broadcast FC10 error must be silent: %s", modbus.Hex(got))
	}
}

func TestSlavePanicRecovery(t *testing.T) {
	bank := &fakeBank{panicRead: true}
	s := newSlave(bank)
	if got := s.Handle(frame(1, 0x04, 0x00, 0x00, 0x00, 0x01)); !bytes.Equal(got, exception(1, 0x04, 0x04)) {
		t.Fatalf("read panic = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(0xFF, 0x04, 0x00, 0x00, 0x00, 0x01)); !bytes.Equal(got, exception(1, 0x04, 0x04)) {
		t.Fatalf("broadcast read panic = %s", modbus.Hex(got))
	}
	s.BcastReplyOwn = false
	if got := s.Handle(frame(0xFF, 0x04, 0x00, 0x00, 0x00, 0x01)); !bytes.Equal(got, exception(0xFF, 0x04, 0x04)) {
		t.Fatalf("broadcast read panic ff = %s", modbus.Hex(got))
	}

	writeBank := &fakeBank{panicWrite: true}
	ws := newSlave(writeBank)
	if got := ws.Handle(frame(1, 0x06, 0x00, 0x10, 0x00, 0x01)); !bytes.Equal(got, exception(1, 0x06, 0x04)) {
		t.Fatalf("write panic = %s", modbus.Hex(got))
	}
	if got := ws.Handle(frame(0xFF, 0x06, 0x00, 0x10, 0x00, 0x01)); got != nil {
		t.Fatalf("broadcast write panic must be silent: %s", modbus.Hex(got))
	}
	if got := ws.Handle(frame(0x00, 0x06, 0x00, 0x10, 0x00, 0x01)); got != nil {
		t.Fatalf("standard broadcast write panic must be silent: %s", modbus.Hex(got))
	}
}

func TestSlaveAdoptRanges(t *testing.T) {
	for _, value := range []uint16{0, 248, 300} {
		bank := &fakeBank{}
		called := false
		s := &modbus.Slave{
			Addr: 1, Bank: bank, AllowAdopt: true,
			OnAdopt: func(uint8) error { called = true; return nil },
		}
		s.Handle(frame(1, 0x06, 0x00, 0x01, byte(value>>8), byte(value)))
		if called {
			t.Fatalf("adopt called for out-of-range value %d", value)
		}
	}
	bank := &fakeBank{}
	s := &modbus.Slave{Addr: 1, Bank: bank, AllowAdopt: true}
	if got := s.Handle(frame(1, 0x06, 0x00, 0x01, 0x00, 0x05)); !bytes.Equal(got, frame(1, 0x06, 0x00, 0x01, 0x00, 0x05)) {
		t.Fatalf("adopt with nil callback echo = %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(1, 0x06, 0x00, 0x02, 0x00, 0x05)); !bytes.Equal(got, frame(1, 0x06, 0x00, 0x02, 0x00, 0x05)) {
		t.Fatalf("non-address register echo = %s", modbus.Hex(got))
	}
}

func TestSlaveAddressFilter(t *testing.T) {
	bank := &fakeBank{}
	s := newSlave(bank)
	if got := s.Handle(frame(9, 0x03, 0x00, 0x00, 0x00, 0x01)); got != nil {
		t.Fatalf("foreign address should be ignored: %s", modbus.Hex(got))
	}
	if got := s.Handle(frame(0x00, 0x03, 0x00, 0x00, 0x00, 0x01)); got != nil {
		t.Fatalf("standard broadcast read should be ignored: %s", modbus.Hex(got))
	}
	req := frame(0x00, 0x06, 0x00, 0x10, 0x00, 0x07)
	if got := s.Handle(req); got != nil {
		t.Fatalf("standard broadcast write should be silent: %s", modbus.Hex(got))
	}
	if bank.wroteHolding[0x10] != 7 {
		t.Fatal("standard broadcast write not applied")
	}
}
