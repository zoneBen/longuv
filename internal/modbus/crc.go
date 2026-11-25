// Package modbus implements the Modbus RTU framing and a minimal MBAP server
// for the iadu-sim register bank.
package modbus

// CRC16 computes the Modbus RTU CRC (poly 0xA001, init 0xFFFF).
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// AppendCRC returns a new PDU with the little-endian CRC appended.
func AppendCRC(pdu []byte) []byte {
	out := make([]byte, len(pdu)+2)
	copy(out, pdu)
	crc := CRC16(pdu)
	out[len(pdu)] = byte(crc)
	out[len(pdu)+1] = byte(crc >> 8)
	return out
}

// CRCOK reports whether a complete RTU frame carries a valid CRC.
func CRCOK(frame []byte) bool {
	if len(frame) < 4 {
		return false
	}
	want := CRC16(frame[:len(frame)-2])
	got := uint16(frame[len(frame)-2]) | uint16(frame[len(frame)-1])<<8
	return want == got
}
