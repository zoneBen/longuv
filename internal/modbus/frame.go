package modbus

import (
	"encoding/binary"
	"encoding/hex"
	"strings"
)

const (
	// MaxRegisters mirrors the Python reference's register address limit.
	MaxRegisters = 0x6000
	// MaxRTURegisters is the per-request register limit used by the reference
	// implementation (the Modbus specification permits 125 as well).
	MaxRTURegisters = 125
	// MaxRTUBits matches the Python reference, which applies the 125 limit to
	// bit reads too (the specification allows 2000).
	MaxRTUBits = 125

	ExcIllegalFunction = 0x01
	ExcIllegalAddress  = 0x02
	ExcIllegalValue    = 0x03
	ExcDeviceFailure   = 0x04
	ExcGatewayTarget   = 0x0B
)

// FrameLength infers the total RTU frame length from the bytes received so
// far. ready is false when more bytes are needed.
func FrameLength(buf []byte) (need int, ready bool) {
	if len(buf) < 2 {
		return 2, false
	}
	switch buf[1] {
	case 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x08:
		return 8, len(buf) >= 8
	case 0x0F, 0x10:
		if len(buf) < 7 {
			return 7, false
		}
		count := int(binary.BigEndian.Uint16(buf[4:6]))
		byteCount := int(buf[6])
		valid := false
		if buf[1] == 0x0F {
			valid = count >= 1 && count <= 1968 && byteCount == (count+7)/8
		} else {
			valid = count >= 1 && count <= 123 && byteCount == count*2
		}
		if !valid {
			// A malformed count/byte-count pair must not make the parser wait
			// for up to 255 bytes. Treat it as one normal frame so the CRC
			// check fails and the feeder resynchronises byte by byte.
			return 8, len(buf) >= 8
		}
		n := 9 + byteCount
		return n, len(buf) >= n
	default:
		return 8, len(buf) >= 8
	}
}

// Hex renders bytes as uppercase space-separated hexadecimal.
func Hex(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	s := strings.ToUpper(hex.EncodeToString(b))
	var out strings.Builder
	out.Grow(len(s) + len(b) - 1)
	for i := 0; i < len(s); i += 2 {
		if i > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(s[i : i+2])
	}
	return out.String()
}
