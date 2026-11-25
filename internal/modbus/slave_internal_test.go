package modbus

import (
	"bytes"
	"testing"
)

func TestUnpackBitsAndWords(t *testing.T) {
	bits := unpackBits([]byte{0x05, 0x01}, 10)
	want := []bool{true, false, true, false, false, false, false, false, true, false}
	for i := range want {
		if bits[i] != want[i] {
			t.Fatalf("unpackBits[%d] = %v want %v", i, bits[i], want[i])
		}
	}
	words := unpackWords([]byte{0x11, 0x22, 0x33, 0x44}, 2)
	if words[0] != 0x1122 || words[1] != 0x3344 {
		t.Fatalf("unpackWords = %#v", words)
	}
	if len(unpackBits(nil, 0)) != 0 || len(unpackWords(nil, 0)) != 0 {
		t.Fatal("zero-count unpack should return empty slices")
	}
}

func TestIsWriteFC(t *testing.T) {
	writes := map[uint8]bool{0x05: true, 0x06: true, 0x0F: true, 0x10: true}
	for fc := uint8(0); fc < 0x20; fc++ {
		if got := isWriteFC(fc); got != writes[fc] {
			t.Fatalf("isWriteFC(%#x) = %v", fc, got)
		}
	}
}

func TestResponseBuilders(t *testing.T) {
	s := &Slave{Addr: 7, BcastReplyOwn: true}
	if got := s.replyAddr(false); got != 7 {
		t.Fatalf("unicast reply address = %d", got)
	}
	if got := s.replyAddr(true); got != 7 {
		t.Fatalf("own broadcast reply address = %d", got)
	}
	s.BcastReplyOwn = false
	if got := s.replyAddr(true); got != 0xFF {
		t.Fatalf("ff broadcast reply address = %d", got)
	}

	got := s.exception(7, 0x03, 0x02)
	if !bytes.Equal(got, AppendCRC([]byte{7, 0x83, 0x02})) {
		t.Fatalf("exception = % X", got)
	}
	got = s.bitsResponse(7, 0x01, []bool{true, false, true, true, false, false, false, false, true})
	if !bytes.Equal(got, AppendCRC([]byte{7, 0x01, 0x02, 0x0D, 0x01})) {
		t.Fatalf("bitsResponse = % X", got)
	}
	got = s.wordsResponse(7, 0x03, []uint16{0x1122, 0x3344})
	if !bytes.Equal(got, AppendCRC([]byte{7, 0x03, 0x04, 0x11, 0x22, 0x33, 0x44})) {
		t.Fatalf("wordsResponse = % X", got)
	}
	got = s.multiResponse(7, 0x10, 0x0010, 0x0002)
	if !bytes.Equal(got, AppendCRC([]byte{7, 0x10, 0x00, 0x10, 0x00, 0x02})) {
		t.Fatalf("multiResponse = % X", got)
	}
	if got := s.bitsResponse(7, 0x01, nil); !bytes.Equal(got, AppendCRC([]byte{7, 0x01, 0x00})) {
		t.Fatalf("empty bitsResponse = % X", got)
	}
	if got := s.wordsResponse(7, 0x03, nil); !bytes.Equal(got, AppendCRC([]byte{7, 0x03, 0x00})) {
		t.Fatalf("empty wordsResponse = % X", got)
	}
}
