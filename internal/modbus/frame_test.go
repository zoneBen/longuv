package modbus

import "testing"

func TestFrameLength(t *testing.T) {
	cases := []struct {
		buf   []byte
		need  int
		ready bool
	}{
		{nil, 2, false},
		{[]byte{0x01}, 2, false},
		{[]byte{0x01, 0x03, 0, 0, 0, 6}, 8, false},
		{[]byte{0x01, 0x03, 0, 0, 0, 6, 0xC5, 0xC8}, 8, true},
		{[]byte{0x01, 0x10, 0, 0, 0, 2}, 7, false},
		{[]byte{0x01, 0x10, 0, 0, 0, 2, 4, 0, 1, 0, 2}, 13, false},
		{[]byte{0x01, 0x10, 0, 0, 0, 2, 4, 0, 1, 0, 2, 0xAA, 0xBB}, 13, true},
		{[]byte{0x01, 0x0F, 0, 0, 0, 3, 1, 0x07}, 10, false},
		{[]byte{0x01, 0x0F, 0, 0, 0, 3, 1, 0x07, 0xAA, 0xBB}, 10, true},
		{[]byte{0x01, 0x07, 0, 0, 0, 1, 0, 0}, 8, true},
	}
	for i, tc := range cases {
		need, ready := FrameLength(tc.buf)
		if need != tc.need || ready != tc.ready {
			t.Fatalf("case %d: got (%d,%v) want (%d,%v)", i, need, ready, tc.need, tc.ready)
		}
	}
}

func TestHex(t *testing.T) {
	if got := Hex([]byte{0x01, 0xAB, 0xFF}); got != "01 AB FF" {
		t.Fatalf("Hex = %q", got)
	}
	if got := Hex(nil); got != "" {
		t.Fatalf("Hex(nil) = %q", got)
	}
}
