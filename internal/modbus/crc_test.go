package modbus

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestCRCKnownVectors(t *testing.T) {
	cases := []struct {
		pdu  string
		want string
	}{
		// Verified with the Python reference implementation. The Rust design
		// draft's C5CD belongs to the 0x000A variant, not this one.
		{"010300000006", "010300000006C5C8"},
		{"01030000000A", "01030000000AC5CD"},
		{"FF040150000A", "FF040150000A643E"},
		{"01040150000A", "01040150000A71E0"},
		{"010401000003", "010401000003B1F7"},
	}
	for _, tc := range cases {
		pdu, err := hex.DecodeString(tc.pdu)
		if err != nil {
			t.Fatal(err)
		}
		got := strings.ToUpper(hex.EncodeToString(AppendCRC(pdu)))
		if got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.pdu, got, tc.want)
		}
	}
}

func TestCRCOK(t *testing.T) {
	frame, _ := hex.DecodeString("FF040150000A643E")
	if !CRCOK(frame) {
		t.Fatal("valid frame rejected")
	}
	frame[len(frame)-1] ^= 0xFF
	if CRCOK(frame) {
		t.Fatal("corrupted frame accepted")
	}
	if CRCOK([]byte{1, 2}) {
		t.Fatal("short frame accepted")
	}
}
