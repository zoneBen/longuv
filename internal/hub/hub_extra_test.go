package hub_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/model"
	"github.com/zoneBen/longuv/internal/state"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHubGettersAndTick(t *testing.T) {
	h, _ := newTestHub(t)
	if h.Ring() == nil || h.Logger() == nil {
		t.Fatal("ring/logger not wired")
	}
	if h.DefaultUnit() != 1 {
		t.Fatalf("default unit = %d", h.DefaultUnit())
	}
	if h.Uptime() < 0 {
		t.Fatalf("uptime = %v", h.Uptime())
	}
	devices := h.Devices()
	if len(devices) != 1 || devices[0].ID() != "iadu-1" {
		t.Fatalf("devices = %+v", devices)
	}
	h.Tick(time.Now())
	if infos := h.PortInfos(); len(infos) != 2 {
		t.Fatalf("port infos = %d", len(infos))
	} else if infos[0].LastRX != nil || infos[0].LastTX != nil {
		t.Fatal("fresh ports should not have IO timestamps")
	}
}

func TestNewRejectsBadConfigs(t *testing.T) {
	if _, err := hub.New(nil, nil, nil, nil); err == nil {
		t.Fatal("nil config should fail")
	}
	cases := []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"duplicate port", func(c *config.Config) { c.Ports = append(c.Ports, c.Ports[0]) }},
		{"duplicate device id", func(c *config.Config) {
			c.Devices = append(c.Devices, c.Devices[0])
		}},
		{"tcp unit zero", func(c *config.Config) { c.Devices[0].TCPUnitID = 0 }},
		{"tcp unit high", func(c *config.Config) { c.Devices[0].TCPUnitID = 248 }},
		{"duplicate tcp unit", func(c *config.Config) {
			dup := c.Devices[0]
			dup.ID = "other"
			c.Devices = append(c.Devices, dup)
		}},
		{"invalid device u count", func(c *config.Config) { c.Devices[0].UCount = 0 }},
		{"binding device", func(c *config.Config) {
			c.Bindings = append(c.Bindings, config.Binding{Device: "missing", Port: "COM1", UnitID: 3})
		}},
		{"binding port", func(c *config.Config) {
			c.Bindings = append(c.Bindings, config.Binding{Device: "iadu-1", Port: "COM9", UnitID: 3})
		}},
		{"binding unit zero", func(c *config.Config) {
			c.Bindings = append(c.Bindings, config.Binding{Device: "iadu-1", Port: "COM1", UnitID: 0})
		}},
		{"duplicate binding", func(c *config.Config) {
			c.Bindings = append(c.Bindings, config.Binding{Device: "iadu-1", Port: "COM1", UnitID: 1})
		}},
	}
	for _, tc := range cases {
		cfg := testConfig(t)
		tc.mutate(cfg)
		if _, err := hub.New(cfg, nil, discardLogger(), nil); err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

func TestNewFromStateVersionValidation(t *testing.T) {
	cfg := testConfig(t)
	if _, err := hub.NewFromState(cfg, state.File{Version: 99}, nil, discardLogger(), nil); err == nil {
		t.Fatal("unsupported state version should fail")
	}
	if _, err := hub.NewFromState(cfg, state.File{
		Version: state.CurrentVersion,
		Devices: []model.Snapshot{{ID: "bad", UCount: 0}},
	}, nil, discardLogger(), nil); err == nil {
		t.Fatal("invalid snapshot should fail")
	}
}

func TestSetTCPUnitAndSortedBindings(t *testing.T) {
	h, cfg := newTestHub(t)
	if err := h.SetTCPUnit("iadu-1", 9); err != nil {
		t.Fatalf("SetTCPUnit: %v", err)
	}
	if d, ok := h.DeviceByTCPUnit(9); !ok || d.ID() != "iadu-1" {
		t.Fatal("new tcp unit not resolvable")
	}
	if err := h.SetTCPUnit("iadu-1", 0); err == nil {
		t.Fatal("unit 0 should be rejected")
	}
	if err := h.SetTCPUnit("iadu-1", 248); err == nil {
		t.Fatal("unit 248 should be rejected")
	}
	if err := h.SetTCPUnit("missing", 5); err == nil {
		t.Fatal("missing device should be rejected")
	}

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
	if err := h.SetTCPUnit("iadu-1", 2); err == nil {
		t.Fatal("conflicting tcp unit should be rejected")
	}
	if err := h.AddBinding(binding("iadu-2", "COM2", 7)); err != nil {
		t.Fatal(err)
	}
	if err := h.AddBinding(binding("iadu-1", "COM2", 2)); err != nil {
		t.Fatal(err)
	}
	sorted := h.BindingsSorted()
	want := []struct {
		port string
		unit uint8
	}{{"COM1", 1}, {"COM2", 1}, {"COM2", 2}, {"COM2", 7}}
	if len(sorted) != len(want) {
		t.Fatalf("sorted bindings = %+v", sorted)
	}
	for i := range want {
		if sorted[i].Port != want[i].port || sorted[i].UnitID != want[i].unit {
			t.Fatalf("sorted[%d] = %+v want %+v", i, sorted[i], want[i])
		}
	}
}

func TestHubErrorBranches(t *testing.T) {
	h, cfg := newTestHub(t)
	if _, ok := h.PortRuntime("missing"); ok {
		t.Fatal("unexpected port runtime")
	}
	if _, ok := h.PortRuntime("COM1"); !ok {
		t.Fatal("COM1 runtime missing")
	}
	h.SetConnected("missing", true)
	h.SetSlavePath("missing", "/dev/null")
	h.RecordRX("missing", 1)
	h.RecordTX("missing", 1)
	h.RecordCRCError("missing")

	if err := h.AddBinding(binding("iadu-1", "COM1", 0)); err == nil {
		t.Fatal("unit 0 binding should fail")
	}
	if err := h.RemoveBinding("iadu-1", "COM1", 42); err == nil {
		t.Fatal("missing binding removal should fail")
	}
	if err := h.Rebind("COM1", 1, 1); err != nil {
		t.Fatalf("same-unit rebind should be a no-op: %v", err)
	}
	if err := h.Rebind("COM1", 1, 0); err == nil {
		t.Fatal("rebind to unit 0 should fail")
	}
	if err := h.Rebind("COM1", 9, 3); err == nil {
		t.Fatal("rebind of a missing old unit should fail")
	}

	seed := cfg.Devices[0]
	seed.ID = "bad-device"
	seed.TCPUnitID = 3
	snap, err := hub.SnapshotFromConfig(seed)
	if err != nil {
		t.Fatal(err)
	}
	snap.UCount = 0
	if err := h.AddDevice(snap); err == nil {
		t.Fatal("invalid device snapshot should fail")
	}
}

func TestHubChangeCallback(t *testing.T) {
	cfg := testConfig(t)
	calls := 0
	h, err := hub.New(cfg, framelog.New(16), discardLogger(), func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := h.Device("iadu-1")
	dev.SetName("changed")
	if calls == 0 {
		t.Fatal("device change did not trigger the callback")
	}
	before := calls
	if err := h.AddBinding(binding("iadu-1", "COM2", 6)); err != nil {
		t.Fatal(err)
	}
	if calls <= before {
		t.Fatal("AddBinding did not trigger the callback")
	}
	before = calls
	if err := h.Rebind("COM2", 6, 7); err != nil {
		t.Fatal(err)
	}
	if calls <= before {
		t.Fatal("Rebind did not trigger the callback")
	}
	before = calls
	if err := h.RemoveBinding("iadu-1", "COM2", 7); err != nil {
		t.Fatal(err)
	}
	if calls <= before {
		t.Fatal("RemoveBinding did not trigger the callback")
	}
}

func TestSnapshotFileIsACopy(t *testing.T) {
	h, _ := newTestHub(t)
	file := h.SnapshotFile()
	if len(file.Bindings) == 0 {
		t.Fatal("no bindings in snapshot")
	}
	file.Bindings[0].DeviceID = "mutated"
	if got := h.Bindings()[0].DeviceID; got == "mutated" {
		t.Fatal("SnapshotFile exposed the live binding slice")
	}
}
