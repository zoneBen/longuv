package modbus

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// MBAPHeaderLen is the fixed MBAP header size: transaction, protocol,
	// length and unit id.
	MBAPHeaderLen = 7
	// MaxMBAPLength is the largest allowed MBAP length field (unit + PDU).
	MaxMBAPLength = 254
)

// MBAP is one Modbus TCP application data unit.
type MBAP struct {
	TxID    uint16
	ProtoID uint16
	UnitID  uint8
	PDU     []byte
}

// ReadMBAP reads one complete MBAP frame.
func ReadMBAP(r io.Reader) (*MBAP, error) {
	var hdr [MBAPHeaderLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	txID := binary.BigEndian.Uint16(hdr[0:2])
	protoID := binary.BigEndian.Uint16(hdr[2:4])
	length := int(binary.BigEndian.Uint16(hdr[4:6]))
	unitID := hdr[6]
	if protoID != 0 {
		return nil, fmt.Errorf("MBAP 协议号必须为 0，得到 %d", protoID)
	}
	if length < 2 || length > MaxMBAPLength {
		return nil, fmt.Errorf("MBAP 长度字段 %d 非法", length)
	}
	pdu := make([]byte, length-1)
	if _, err := io.ReadFull(r, pdu); err != nil {
		return nil, err
	}
	return &MBAP{TxID: txID, ProtoID: protoID, UnitID: unitID, PDU: pdu}, nil
}

// WriteMBAP writes one complete MBAP frame.
func WriteMBAP(w io.Writer, m *MBAP) error {
	if len(m.PDU) == 0 || len(m.PDU) > MaxMBAPLength-1 {
		return fmt.Errorf("PDU 长度 %d 非法", len(m.PDU))
	}
	var hdr [MBAPHeaderLen]byte
	binary.BigEndian.PutUint16(hdr[0:2], m.TxID)
	binary.BigEndian.PutUint16(hdr[2:4], 0)
	binary.BigEndian.PutUint16(hdr[4:6], uint16(1+len(m.PDU)))
	hdr[6] = m.UnitID
	if err := writeAll(w, hdr[:]); err != nil {
		return err
	}
	return writeAll(w, m.PDU)
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
