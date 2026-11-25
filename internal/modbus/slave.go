package modbus

import "encoding/binary"

// UnitDevice is the register bank surface needed by the RTU slave.
type UnitDevice interface {
	ReadInput(start, count uint16, unit uint8) ([]uint16, uint8)
	ReadHolding(start, count uint16, unit uint8) ([]uint16, uint8)
	ReadCoils(start, count uint16) ([]bool, uint8)
	ReadDiscrete(start, count uint16) ([]bool, uint8)
	WriteHolding(addr, value uint16, unit uint8) uint8
	WriteHoldingMany(start uint16, values []uint16, unit uint8) uint8
	WriteCoil(addr uint16, on bool) uint8
	WriteCoilsMany(start uint16, values []bool) uint8
}

// Slave handles RTU frames for one (port, unit) binding.
type Slave struct {
	Addr          uint8
	Bank          UnitDevice
	BcastReplyOwn bool
	AllowAdopt    bool
	OnAdopt       func(newUnit uint8) error
}

// Handle processes one complete RTU frame. It returns nil when no response
// should be sent (address mismatch, bad CRC, or a broadcast write).
func (s *Slave) Handle(frame []byte) (resp []byte) {
	if len(frame) < 4 || !CRCOK(frame) {
		return nil
	}
	addr, fc := frame[0], frame[1]
	bcastFF := addr == 0xFF
	stdBcast := addr == 0x00
	if !bcastFF && !stdBcast && addr != s.Addr {
		return nil
	}
	defer func() {
		if recover() == nil {
			return
		}
		if stdBcast || (bcastFF && isWriteFC(fc)) {
			resp = nil
			return
		}
		resp = s.exception(s.replyAddr(bcastFF), fc, ExcDeviceFailure)
	}()

	pdu := frame[2 : len(frame)-2]

	switch fc {
	case 0x01, 0x02:
		if stdBcast {
			return nil
		}
		if len(pdu) != 4 {
			return s.exception(s.replyAddr(bcastFF), fc, ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[0:2])
		count := binary.BigEndian.Uint16(pdu[2:4])
		if count < 1 || count > MaxRTUBits || int(start)+int(count) > MaxRegisters {
			return s.exception(s.replyAddr(bcastFF), fc, ExcIllegalAddress)
		}
		var bits []bool
		var code uint8
		if fc == 0x01 {
			bits, code = s.Bank.ReadCoils(start, count)
		} else {
			bits, code = s.Bank.ReadDiscrete(start, count)
		}
		if code != 0 {
			return s.exception(s.replyAddr(bcastFF), fc, code)
		}
		return s.bitsResponse(s.replyAddr(bcastFF), fc, bits)

	case 0x03, 0x04:
		if stdBcast {
			return nil
		}
		if len(pdu) != 4 {
			return s.exception(s.replyAddr(bcastFF), fc, ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[0:2])
		count := binary.BigEndian.Uint16(pdu[2:4])
		if count < 1 || count > MaxRTURegisters || int(start)+int(count) > MaxRegisters {
			return s.exception(s.replyAddr(bcastFF), fc, ExcIllegalAddress)
		}
		var words []uint16
		var code uint8
		if fc == 0x03 {
			words, code = s.Bank.ReadHolding(start, count, s.Addr)
		} else {
			words, code = s.Bank.ReadInput(start, count, s.Addr)
		}
		if code != 0 {
			return s.exception(s.replyAddr(bcastFF), fc, code)
		}
		return s.wordsResponse(s.replyAddr(bcastFF), fc, words)

	case 0x05, 0x06:
		if len(pdu) != 4 {
			if bcastFF || stdBcast {
				return nil
			}
			return s.exception(s.Addr, fc, ExcIllegalValue)
		}
		reg := binary.BigEndian.Uint16(pdu[0:2])
		val := binary.BigEndian.Uint16(pdu[2:4])
		var code uint8
		if fc == 0x05 {
			code = s.Bank.WriteCoil(reg, val == 0xFF00)
		} else {
			code = s.Bank.WriteHolding(reg, val, s.Addr)
			if reg == 0x0001 && s.AllowAdopt && val >= 1 && val <= 247 && s.OnAdopt != nil {
				_ = s.OnAdopt(uint8(val))
			}
		}
		if bcastFF || stdBcast {
			return nil
		}
		if code != 0 {
			return s.exception(s.Addr, fc, code)
		}
		return append([]byte(nil), frame...)

	case 0x0F:
		if len(pdu) < 5 {
			if bcastFF || stdBcast {
				return nil
			}
			return s.exception(s.Addr, fc, ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[0:2])
		count := binary.BigEndian.Uint16(pdu[2:4])
		byteCount := int(pdu[4])
		if count < 1 || count > 1968 || int(start)+int(count) > MaxRegisters ||
			byteCount != int(count+7)/8 || byteCount != len(pdu)-5 {
			if bcastFF || stdBcast {
				return nil
			}
			return s.exception(s.Addr, fc, ExcIllegalValue)
		}
		values := unpackBits(pdu[5:], int(count))
		code := s.Bank.WriteCoilsMany(start, values)
		if bcastFF || stdBcast {
			return nil
		}
		if code != 0 {
			return s.exception(s.Addr, fc, code)
		}
		return s.multiResponse(s.Addr, fc, start, count)

	case 0x10:
		if len(pdu) < 5 {
			if bcastFF || stdBcast {
				return nil
			}
			return s.exception(s.Addr, fc, ExcIllegalValue)
		}
		start := binary.BigEndian.Uint16(pdu[0:2])
		count := binary.BigEndian.Uint16(pdu[2:4])
		byteCount := int(pdu[4])
		if count < 1 || count > 123 || int(start)+int(count) > MaxRegisters ||
			byteCount != int(count)*2 || byteCount != len(pdu)-5 {
			if bcastFF || stdBcast {
				return nil
			}
			return s.exception(s.Addr, fc, ExcIllegalValue)
		}
		values := unpackWords(pdu[5:], int(count))
		code := s.Bank.WriteHoldingMany(start, values, s.Addr)
		if bcastFF || stdBcast {
			return nil
		}
		if code != 0 {
			return s.exception(s.Addr, fc, code)
		}
		return s.multiResponse(s.Addr, fc, start, count)

	default:
		if bcastFF || stdBcast {
			return nil
		}
		return s.exception(s.Addr, fc, ExcIllegalFunction)
	}
}

func (s *Slave) replyAddr(bcastFF bool) uint8 {
	if bcastFF {
		if s.BcastReplyOwn {
			return s.Addr
		}
		return 0xFF
	}
	return s.Addr
}

func (s *Slave) exception(addr, fc, code uint8) []byte {
	return AppendCRC([]byte{addr, fc | 0x80, code})
}

func (s *Slave) bitsResponse(addr, fc uint8, bits []bool) []byte {
	byteCount := (len(bits) + 7) / 8
	out := make([]byte, 3+byteCount)
	out[0], out[1], out[2] = addr, fc, byte(byteCount)
	for i, v := range bits {
		if v {
			out[3+i/8] |= 1 << uint(i%8)
		}
	}
	return AppendCRC(out)
}

func (s *Slave) wordsResponse(addr, fc uint8, words []uint16) []byte {
	out := make([]byte, 3+2*len(words))
	out[0], out[1], out[2] = addr, fc, byte(2*len(words))
	for i, v := range words {
		binary.BigEndian.PutUint16(out[3+2*i:], v)
	}
	return AppendCRC(out)
}

func (s *Slave) multiResponse(addr, fc uint8, start, count uint16) []byte {
	out := make([]byte, 6)
	out[0], out[1] = addr, fc
	binary.BigEndian.PutUint16(out[2:], start)
	binary.BigEndian.PutUint16(out[4:], count)
	return AppendCRC(out)
}

func unpackBits(data []byte, count int) []bool {
	out := make([]bool, count)
	for i := 0; i < count; i++ {
		out[i] = data[i/8]&(1<<uint(i%8)) != 0
	}
	return out
}

func unpackWords(data []byte, count int) []uint16 {
	out := make([]uint16, count)
	for i := 0; i < count; i++ {
		out[i] = binary.BigEndian.Uint16(data[2*i:])
	}
	return out
}

func isWriteFC(fc uint8) bool {
	switch fc {
	case 0x05, 0x06, 0x0F, 0x10:
		return true
	}
	return false
}
