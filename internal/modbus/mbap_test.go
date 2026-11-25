package modbus

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestMBAPRoundTrip(t *testing.T) {
	pdu, err := hex.DecodeString("0300000001")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteMBAP(&buf, &MBAP{TxID: 0x1234, UnitID: 1, PDU: pdu}); err != nil {
		t.Fatal(err)
	}
	want := "123400000006010300000001"
	if got := strings.ToUpper(hex.EncodeToString(buf.Bytes())); got != want {
		t.Fatalf("encoded = %s want %s", got, want)
	}
	m, err := ReadMBAP(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if m.TxID != 0x1234 || m.UnitID != 1 || !bytes.Equal(m.PDU, pdu) {
		t.Fatalf("decoded = %+v", m)
	}
}

func TestMBAPRejectsProtocolID(t *testing.T) {
	raw, _ := hex.DecodeString("12340001000201FF")
	if _, err := ReadMBAP(bytes.NewReader(raw)); err == nil {
		t.Fatal("non-zero protocol id accepted")
	}
}

func TestMBAPRejectsBadLength(t *testing.T) {
	for _, raw := range []string{
		"12340000000001", // length 0
		"1234000000FF01", // length 255
	} {
		b, _ := hex.DecodeString(raw)
		if _, err := ReadMBAP(bytes.NewReader(b)); err == nil {
			t.Fatalf("%s accepted", raw)
		}
	}
}

func TestMBAPShortRead(t *testing.T) {
	b, _ := hex.DecodeString("1234")
	if _, err := ReadMBAP(bytes.NewReader(b)); err == nil {
		t.Fatal("short header accepted")
	}
}

func TestMBAPWriteValidation(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMBAP(&buf, &MBAP{TxID: 1, UnitID: 1}); err == nil {
		t.Fatal("empty PDU accepted")
	}
	if err := WriteMBAP(&buf, &MBAP{TxID: 1, UnitID: 1, PDU: make([]byte, 254)}); err == nil {
		t.Fatal("oversized PDU accepted")
	}
}
