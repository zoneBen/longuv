package transport_test

import (
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/modbus"
	"github.com/zoneBen/longuv/internal/transport"
)

func startMBAP(t *testing.T, h *hub.Hub, cfg *config.Config) *transport.MBAPServer {
	t.Helper()
	tcpCfg := cfg.Server.ModbusTCP
	tcpCfg.Listen = "127.0.0.1:0"
	tcpCfg.MaxConns = 8
	tcpCfg.IdleTimeoutS = 5
	srv := transport.NewMBAPServer(tcpCfg, h, h.Ring(), discardLogger())
	if err := srv.Start(); err != nil {
		t.Fatalf("start MBAP server: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func dialMBAP(t *testing.T, srv *transport.MBAPServer) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	return conn
}

func transact(t *testing.T, conn net.Conn, txID uint16, unit uint8, pduHex string) []byte {
	t.Helper()
	pdu := mustHex(t, pduHex)
	if err := modbus.WriteMBAP(conn, &modbus.MBAP{TxID: txID, UnitID: unit, PDU: pdu}); err != nil {
		t.Fatalf("write: %v", err)
	}
	m, err := modbus.ReadMBAP(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if m.TxID != txID {
		t.Fatalf("tx id = %d want %d", m.TxID, txID)
	}
	if m.UnitID != unit {
		t.Fatalf("unit id = %d want %d", m.UnitID, unit)
	}
	return m.PDU
}

func expectPDU(t *testing.T, got []byte, wantHex string) {
	t.Helper()
	want := mustHex(t, wantHex)
	if strings.ToUpper(hex.EncodeToString(got)) != strings.ToUpper(hex.EncodeToString(want)) {
		t.Fatalf("\n got: %X\nwant: %X", got, want)
	}
}

func TestMBAPShadowReads(t *testing.T) {
	h, _, cfg := newFeederEnv(t)
	srv := startMBAP(t, h, cfg)
	conn := dialMBAP(t, srv)

	expectPDU(t, transact(t, conn, 1, 1, "0380000006"),
		"030C802000010003002A01000100")
	expectPDU(t, transact(t, conn, 2, 1, "0480100004"),
		"0408E200000000000001")
	expectPDU(t, transact(t, conn, 3, 1, "0380C00003"),
		"0306856085608560")
	// "iADU-SIM-00001" padded to 20 ASCII bytes with six spaces.
	expectPDU(t, transact(t, conn, 4, 1, "038006000A"),
		"0314"+strings.ToUpper("694144552D53494D2D3030303031202020202020"))
}

func TestMBAPShadowWrites(t *testing.T) {
	h, _, cfg := newFeederEnv(t)
	srv := startMBAP(t, h, cfg)
	conn := dialMBAP(t, srv)
	dev, _ := h.Device("iadu-1")

	expectPDU(t, transact(t, conn, 10, 1, "0680D000A0"), "0680D000A0")
	expectPDU(t, transact(t, conn, 11, 1, "0380D00001"), "030200A0")
	words, code := dev.ReadHolding(0x0100, 1, 1)
	if code != 0 || words[0] != 0x00A0 {
		t.Fatalf("native LED = %#v code=%d", words, code)
	}

	expectPDU(t, transact(t, conn, 12, 1, "058000FF00"), "058000FF00")
	expectPDU(t, transact(t, conn, 13, 1, "0180000004"), "01010F")

	expectPDU(t, transact(t, conn, 14, 1, "0F800200010100"), "0F80020001")
	expectPDU(t, transact(t, conn, 15, 1, "0180000004"), "01010B")

	expectPDU(t, transact(t, conn, 16, 1, "108010000204AAAABBBB"), "1080100002")
	expectPDU(t, transact(t, conn, 17, 1, "0380100002"), "0304AAAABBBB")

	expectPDU(t, transact(t, conn, 18, 1, "0680C08A70"), "0680C08A70")
	words, _ = dev.ReadInput(0x0100, 1, 1)
	if words[0] != 0x8A70 {
		t.Fatalf("native TH = %#x", words[0])
	}
	if dev.ManualMask()&1 == 0 {
		t.Fatal("TH write should freeze the top zone")
	}
	expectPDU(t, transact(t, conn, 19, 1, "0681200002"), "0681200002")
	if dev.ManualMask() != 0 {
		t.Fatal("command 2 should clear manual overrides")
	}
}

func TestMBAPUnitsAndErrors(t *testing.T) {
	h, _, cfg := newFeederEnv(t)
	srv := startMBAP(t, h, cfg)
	conn := dialMBAP(t, srv)

	expectPDU(t, transact(t, conn, 20, 9, "0380000006"), "830B")
	expectPDU(t, transact(t, conn, 21, 0, "0380000006"),
		"030C802000010003002A01000100")
	expectPDU(t, transact(t, conn, 22, 0xFF, "0380000006"),
		"030C802000010003002A01000100")
	expectPDU(t, transact(t, conn, 23, 1, "2B"), "AB01")
	expectPDU(t, transact(t, conn, 24, 1, "0380000000"), "8303")
	expectPDU(t, transact(t, conn, 25, 1, "0680000001"), "8602")
	expectPDU(t, transact(t, conn, 26, 1, "0300000001"), "8302")
}

func TestMBAPMultiClientAndStats(t *testing.T) {
	h, _, cfg := newFeederEnv(t)
	srv := startMBAP(t, h, cfg)
	c1 := dialMBAP(t, srv)
	c2 := dialMBAP(t, srv)

	expectPDU(t, transact(t, c1, 30, 1, "0380C00003"), "0306856085608560")
	expectPDU(t, transact(t, c2, 31, 1, "0380000006"),
		"030C802000010003002A01000100")
	expectPDU(t, transact(t, c1, 32, 1, "0480100004"), "0408E200000000000001")

	stats := srv.Stats()
	if stats.RX < 3 || stats.TX < 3 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.ActiveConns != 2 {
		t.Fatalf("active conns = %d", stats.ActiveConns)
	}
}
