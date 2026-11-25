package hub_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/model"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default(t.TempDir())
	disabled := false
	cfg.Devices[0].Climate.Enabled = &disabled
	cfg.Devices[0].Fixed = config.FixedTH{Temp: 26.5, Humi: 48.0}
	cfg.Devices[0].ESN = "iADU-SIM-00001"
	return cfg
}

func newTestHub(t *testing.T) (*hub.Hub, *config.Config) {
	t.Helper()
	cfg := testConfig(t)
	h, err := hub.New(cfg, framelog.New(128), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	return h, cfg
}

func TestHubSeedLookups(t *testing.T) {
	h, _ := newTestHub(t)
	dev, ok := h.Device("iadu-1")
	if !ok {
		t.Fatal("device iadu-1 missing")
	}
	if dev.PrimaryAddress() != 1 {
		t.Fatalf("primary address = %d", dev.PrimaryAddress())
	}
	if b, ok := h.BindingFor("COM1", 1); !ok || b.DeviceID != "iadu-1" {
		t.Fatalf("COM1/1 binding = %+v", b)
	}
	if _, ok := h.BindingFor("COM1", 2); ok {
		t.Fatal("unexpected COM1/2 binding")
	}
	if got := len(h.BindingsForPort("COM1")); got != 1 {
		t.Fatalf("COM1 bindings = %d", got)
	}
	if d, ok := h.DeviceByTCPUnit(1); !ok || d.ID() != "iadu-1" {
		t.Fatal("tcp unit 1 lookup failed")
	}
	if _, ok := h.DeviceByTCPUnit(2); ok {
		t.Fatal("unexpected tcp unit 2")
	}
}

func TestSnapshotFromConfigGeneration(t *testing.T) {
	h, _ := newTestHub(t)
	dev, _ := h.Device("iadu-1")
	snap := dev.Snapshot()
	if snap.Tags[0] != "E200000000000001" {
		t.Fatalf("U1 tag = %s", snap.Tags[0])
	}
	if snap.Tags[10] != "0000000000000000" {
		t.Fatalf("U11 should be absent, got %s", snap.Tags[10])
	}
	if snap.Tags[19] != "E200000000000014" {
		t.Fatalf("U20 tag = %s", snap.Tags[19])
	}
	var wantPresent uint64
	for u := 1; u <= 10; u++ {
		wantPresent |= 1 << uint(u-1)
	}
	wantPresent |= 1 << 19
	if snap.Present != wantPresent {
		t.Fatalf("present = %#x want %#x", snap.Present, wantPresent)
	}
	if snap.Bus != [3]uint16{1, 1, 0} {
		t.Fatalf("bus = %#v", snap.Bus)
	}
}

func TestPortInfosAndStats(t *testing.T) {
	h, _ := newTestHub(t)
	infos := h.PortInfos()
	if len(infos) != 2 || infos[0].Name != "COM1" {
		t.Fatalf("ports = %+v", infos)
	}
	h.SetSlavePath("COM1", "/dev/pts/99")
	h.SetConnected("COM1", true)
	h.RecordRX("COM1", 8)
	h.RecordTX("COM1", 25)
	h.RecordCRCError("COM1")
	infos = h.PortInfos()
	com1 := infos[0]
	if com1.SlavePath != "/dev/pts/99" || !com1.Connected {
		t.Fatalf("runtime = %+v", com1)
	}
	if com1.RX != 1 || com1.TX != 1 || com1.RXBytes != 8 || com1.TXBytes != 25 || com1.CRCErrors != 1 {
		t.Fatalf("counters = %+v", com1)
	}
	if com1.LastRX == nil || com1.LastTX == nil {
		t.Fatal("timestamps missing")
	}
}

func TestDeviceAndBindingLifecycle(t *testing.T) {
	h, cfg := newTestHub(t)
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
		t.Fatalf("AddDevice: %v", err)
	}
	if d, ok := h.DeviceByTCPUnit(2); !ok || d.ID() != "iadu-2" {
		t.Fatal("second device not registered")
	}
	if err := h.AddDevice(snap); err == nil {
		t.Fatal("duplicate device id accepted")
	}
	conflict := snap
	conflict.ID = "iadu-3"
	if err := h.AddDevice(conflict); err == nil {
		t.Fatal("duplicate tcp unit accepted")
	}

	if err := h.AddBinding(binding("iadu-2", "COM1", 2)); err != nil {
		t.Fatalf("AddBinding: %v", err)
	}
	if err := h.AddBinding(binding("iadu-2", "COM1", 2)); err == nil {
		t.Fatal("duplicate port/unit accepted")
	}
	if err := h.AddBinding(binding("missing", "COM1", 3)); err == nil {
		t.Fatal("missing device accepted")
	}
	if err := h.AddBinding(binding("iadu-2", "COM9", 3)); err == nil {
		t.Fatal("missing port accepted")
	}

	if err := h.Rebind("COM1", 1, 5); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if _, ok := h.BindingFor("COM1", 1); ok {
		t.Fatal("old address still bound")
	}
	if b, ok := h.BindingFor("COM1", 5); !ok || b.DeviceID != "iadu-1" {
		t.Fatalf("new binding = %+v", b)
	}
	dev, _ := h.Device("iadu-1")
	if dev.PrimaryAddress() != 5 {
		t.Fatalf("primary address = %d", dev.PrimaryAddress())
	}
	if err := h.Rebind("COM1", 5, 2); err == nil {
		t.Fatal("rebind onto an occupied address accepted")
	}

	if err := h.RemoveBinding("iadu-2", "COM1", 2); err != nil {
		t.Fatalf("RemoveBinding: %v", err)
	}
	if err := h.RemoveDevice("iadu-2"); err != nil {
		t.Fatalf("RemoveDevice: %v", err)
	}
	if _, ok := h.Device("iadu-2"); ok {
		t.Fatal("device still present")
	}
	if err := h.RemoveDevice("iadu-2"); err == nil {
		t.Fatal("removing a missing device should fail")
	}
}

func binding(deviceID, port string, unit uint8) model.Binding {
	return model.Binding{DeviceID: deviceID, Port: port, UnitID: unit}
}
