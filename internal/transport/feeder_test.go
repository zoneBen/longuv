package transport_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/modbus"
	"github.com/zoneBen/longuv/internal/model"
	"github.com/zoneBen/longuv/internal/transport"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newFeederEnv(t *testing.T) (*hub.Hub, *transport.Feeder, *config.Config) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	disabled := false
	cfg.Devices[0].Climate.Enabled = &disabled
	cfg.Devices[0].Fixed = config.FixedTH{Temp: 26.5, Humi: 48.0}
	cfg.Devices[0].ESN = "iADU-SIM-00001"
	ring := framelog.New(256)
	h, err := hub.New(cfg, ring, discardLogger(), nil)
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	feeder := transport.NewFeeder(cfg.Ports[0], h, ring, discardLogger())
	return h, feeder, cfg
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex %q: %v", s, err)
	}
	return b
}

func TestFeederGoldenFrames(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "modbus", "testdata", "rtu_golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var gf struct {
		Cases []struct {
			Name     string `json:"name"`
			Request  string `json:"request"`
			Response string `json:"response"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &gf); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	h, feeder, _ := newFeederEnv(t)
	for _, tc := range gf.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			pending := mustHex(t, tc.Request)
			got := feeder.Feed(&pending)
			if len(pending) != 0 {
				t.Fatalf("leftover bytes: %s", modbus.Hex(pending))
			}
			if tc.Response == "" {
				if len(got) != 0 {
					t.Fatalf("expected no response, got %s", modbus.Hex(got))
				}
				return
			}
			want := mustHex(t, tc.Response)
			if !bytes.Equal(got, want) {
				t.Fatalf("\n got: %s\nwant: %s", modbus.Hex(got), modbus.Hex(want))
			}
		})
	}
	// The feeder records RX/TX statistics.
	rt := portRuntime(t, h, "COM1")
	if rt.RX.Load() == 0 || rt.TX.Load() == 0 {
		t.Fatalf("stats not recorded: rx=%d tx=%d", rt.RX.Load(), rt.TX.Load())
	}
}

func TestFeederChunkedInput(t *testing.T) {
	_, feeder, _ := newFeederEnv(t)
	req := modbus.AppendCRC(mustHex(t, "010401000003"))
	want := mustHex(t, "010406856085608560B4A4")
	var pending []byte
	for i := 0; i < len(req); i++ {
		pending = append(pending, req[i])
		out := feeder.Feed(&pending)
		if i < len(req)-1 && len(out) != 0 {
			t.Fatalf("early response at byte %d: %s", i, modbus.Hex(out))
		}
		if i == len(req)-1 && !bytes.Equal(out, want) {
			t.Fatalf("\n got: %s\nwant: %s", modbus.Hex(out), modbus.Hex(want))
		}
	}
}

func TestFeederResyncsAfterGarbage(t *testing.T) {
	h, feeder, _ := newFeederEnv(t)
	req := modbus.AppendCRC(mustHex(t, "010401000003"))
	pending := append([]byte{0xAA, 0x55}, req...)
	out := feeder.Feed(&pending)
	want := mustHex(t, "010406856085608560B4A4")
	if !bytes.Equal(out, want) {
		t.Fatalf("\n got: %s\nwant: %s", modbus.Hex(out), modbus.Hex(want))
	}
	if portRuntime(t, h, "COM1").CRCErrors.Load() == 0 {
		t.Fatal("expected CRC errors while resyncing")
	}
}

func TestFeederAdoptionRebindsPort(t *testing.T) {
	h, feeder, _ := newFeederEnv(t)
	adoptReq := modbus.AppendCRC([]byte{0x01, 0x06, 0x00, 0x01, 0x00, 0x05})
	pending := append([]byte(nil), adoptReq...)
	if out := feeder.Feed(&pending); !bytes.Equal(out, adoptReq) {
		t.Fatalf("adopt echo = %s want %s", modbus.Hex(out), modbus.Hex(adoptReq))
	}
	if _, ok := h.BindingFor("COM1", 1); ok {
		t.Fatal("old binding still present")
	}
	if b, ok := h.BindingFor("COM1", 5); !ok || b.DeviceID != "iadu-1" {
		t.Fatalf("new binding = %+v", b)
	}
	req5 := modbus.AppendCRC([]byte{0x05, 0x04, 0x01, 0x00, 0x00, 0x03})
	out5 := feeder.Feed(&req5)
	if len(out5) == 0 || out5[0] != 0x05 {
		t.Fatalf("read at new address failed: %s", modbus.Hex(out5))
	}
	req1 := modbus.AppendCRC([]byte{0x01, 0x04, 0x01, 0x00, 0x00, 0x03})
	if out1 := feeder.Feed(&req1); len(out1) != 0 {
		t.Fatalf("read at old address should be ignored: %s", modbus.Hex(out1))
	}
}

func TestFeederBroadcastModes(t *testing.T) {
	h, feeder, cfg := newFeederEnv(t)
	seed := cfg.Devices[0]
	seed.ID = "iadu-2"
	seed.Name = "second"
	seed.TCPUnitID = 2
	seed.ESN = "iADU-SIM-00002"
	snap, err := hub.SnapshotFromConfig(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AddDevice(snap); err != nil {
		t.Fatal(err)
	}
	if err := h.AddBinding(model.Binding{DeviceID: "iadu-2", Port: "COM1", UnitID: 2}); err != nil {
		t.Fatal(err)
	}

	disc := modbus.AppendCRC(mustHex(t, "FF040150000A"))
	pending := append([]byte(nil), disc...)
	out := feeder.Feed(&pending)
	if len(out) != 50 {
		t.Fatalf("all mode response length = %d (%s)", len(out), modbus.Hex(out))
	}
	if out[0] != 0x01 || out[25] != 0x02 {
		t.Fatalf("response addresses = %d/%d", out[0], out[25])
	}

	feeder.Port.BroadcastMode = "first"
	pending = append([]byte(nil), disc...)
	out = feeder.Feed(&pending)
	if len(out) != 25 || out[0] != 0x01 {
		t.Fatalf("first mode response = %s", modbus.Hex(out))
	}
}

func TestFeederIgnoresUnknownAddress(t *testing.T) {
	h, feeder, _ := newFeederEnv(t)
	req := modbus.AppendCRC([]byte{0x09, 0x03, 0x00, 0x00, 0x00, 0x06})
	if out := feeder.Feed(&req); len(out) != 0 {
		t.Fatalf("unexpected response: %s", modbus.Hex(out))
	}
	if rt := portRuntime(t, h, "COM1"); rt.RX.Load() != 1 {
		t.Fatalf("RX counter = %d", rt.RX.Load())
	}
}

func TestFeederBadCRC(t *testing.T) {
	h, feeder, _ := newFeederEnv(t)
	req := modbus.AppendCRC([]byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x06})
	req[len(req)-1] ^= 0xFF
	if out := feeder.Feed(&req); len(out) != 0 {
		t.Fatalf("unexpected response: %s", modbus.Hex(out))
	}
	if portRuntime(t, h, "COM1").CRCErrors.Load() == 0 {
		t.Fatal("CRC error not counted")
	}
}

func TestFeederFirstFlag(t *testing.T) {
	h, _, cfg := newFeederEnv(t)
	if h.DefaultUnit() != 1 {
		t.Fatalf("default unit = %d", h.DefaultUnit())
	}
	if len(cfg.Ports) != 2 {
		t.Fatalf("ports = %d", len(cfg.Ports))
	}
}

func portRuntime(t *testing.T, h *hub.Hub, name string) *hub.PortRuntime {
	t.Helper()
	rt, ok := h.PortRuntime(name)
	if !ok {
		t.Fatalf("port %s runtime missing", name)
	}
	return rt
}
