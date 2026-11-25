package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigValid(t *testing.T) {
	c := Default(t.TempDir())
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if len(c.Ports) != 2 {
		t.Fatalf("expected 2 default ports, got %d", len(c.Ports))
	}
	if len(c.Devices) != 1 {
		t.Fatalf("expected 1 default device, got %d", len(c.Devices))
	}
	if len(c.Bindings) != 2 {
		t.Fatalf("expected 2 default bindings, got %d", len(c.Bindings))
	}
}

func TestGeneratedYAMLRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "config.yaml")
	if err := WriteDefault(path, dir); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	if err := WriteDefault(path, dir); err == nil {
		t.Fatal("WriteDefault should refuse to overwrite")
	}
	c, err := Load(path, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("round-tripped config invalid: %v", err)
	}
	if c.Devices[0].TCPUnitID != 1 {
		t.Fatalf("expected tcp unit 1, got %d", c.Devices[0].TCPUnitID)
	}
	if c.Server.Web.Listen != "127.0.0.1:18090" {
		t.Fatalf("unexpected web listen %q", c.Server.Web.Listen)
	}
}

func TestOccupancyParseFormat(t *testing.T) {
	all, err := ParseOccupancy("all", 42)
	if err != nil {
		t.Fatal(err)
	}
	if all != (uint64(1)<<42)-1 {
		t.Fatalf("all mask = %#x", all)
	}
	mask, err := ParseOccupancy("1-10,20", 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatOccupancy(mask, 42); got != "1-10,20" {
		t.Fatalf("FormatOccupancy = %q", got)
	}
	var count int
	for i := 0; i < 42; i++ {
		if mask&(1<<uint(i)) != 0 {
			count++
		}
	}
	if count != 11 {
		t.Fatalf("expected 11 set bits, got %d", count)
	}
	if FormatOccupancy(0, 42) != "none" {
		t.Fatal("empty mask should format as none")
	}
	for _, bad := range []string{"0", "43", "10-5", "1,abc"} {
		if _, err := ParseOccupancy(bad, 42); err == nil {
			t.Fatalf("ParseOccupancy(%q) should fail", bad)
		}
	}
}

func TestTagSeedAndESN(t *testing.T) {
	tag, err := ParseTagHex("e200000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if FormatTagHex(tag) != "E200000000000001" {
		t.Fatalf("tag round-trip = %s", FormatTagHex(tag))
	}
	if _, err := ParseTagHex("E200"); err == nil {
		t.Fatal("short tag should fail")
	}
	esn := NormalizeESN("iADU-SIM")
	if string(esn[:8]) != "iADU-SIM" || string(esn[8:]) != strings.Repeat(" ", 12) {
		t.Fatalf("unexpected padded ESN %q", string(esn[:]))
	}
}

func TestAutoAssignTCPUnits(t *testing.T) {
	yaml := `
version: 1
server: {}
ports:
  - {name: COM1, transport: pty, link: /tmp/com1}
devices:
  - {id: a, tcp_unit_id: 7}
  - {id: b}
  - {id: c}
bindings:
  - {device: a, port: COM1, unit_id: 1}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := map[string]uint8{}
	for _, d := range c.Devices {
		got[d.ID] = d.TCPUnitID
	}
	if got["a"] != 7 || got["b"] != 1 || got["c"] != 2 {
		t.Fatalf("unexpected tcp units: %+v", got)
	}
}

func TestDuplicateBindingRejected(t *testing.T) {
	c := Default(t.TempDir())
	c.Bindings = append(c.Bindings, Binding{Device: "iadu-1", Port: "COM1", UnitID: 1})
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate port/unit binding should be rejected")
	}
}

func TestDuplicateTCPUnitRejected(t *testing.T) {
	c := Default(t.TempDir())
	c.Devices = append(c.Devices, defaultDevice(2, c.Server.Timezone))
	c.Devices[1].TCPUnitID = c.Devices[0].TCPUnitID
	if err := c.Validate(); err == nil {
		t.Fatal("duplicate tcp_unit_id should be rejected")
	}
}

func TestInvalidConfigValues(t *testing.T) {
	cases := []func(*Config){
		func(c *Config) { c.Server.Web.Listen = "not-a-listen" },
		func(c *Config) { c.Server.Log.Level = "verbose" },
		func(c *Config) { c.Devices[0].UCount = 43 },
		func(c *Config) { c.Devices[0].ESN = strings.Repeat("X", 21) },
		func(c *Config) { c.Ports[0].BcastReply = "broadcast" },
		func(c *Config) { c.Ports[0].Mode = "9999" },
		func(c *Config) { c.Devices[0].Climate.Temperature.Min = 30 },
	}
	for i, mutate := range cases {
		c := Default(t.TempDir())
		mutate(c)
		if err := c.Validate(); err == nil {
			t.Fatalf("case %d should be invalid", i)
		}
	}
}
