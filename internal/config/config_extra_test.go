package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBooleanHelpers(t *testing.T) {
	if !(Web{}).IsEnabled() || !(ModbusTCP{}).IsEnabled() || !(Climate{}).IsEnabled() {
		t.Fatal("nil enabled pointers should default to true")
	}
	no := false
	yes := true
	if (Web{Enabled: &no}).IsEnabled() || !(Web{Enabled: &yes}).IsEnabled() {
		t.Fatal("Web.IsEnabled mismatch")
	}
	if (ModbusTCP{Enabled: &no}).IsEnabled() || !(ModbusTCP{Enabled: &yes}).IsEnabled() {
		t.Fatal("ModbusTCP.IsEnabled mismatch")
	}
	if (Climate{Enabled: &no}).IsEnabled() || !(Climate{Enabled: &yes}).IsEnabled() {
		t.Fatal("Climate.IsEnabled mismatch")
	}
	if !(Device{}).AdoptsAddress() || (Device{AdoptAddress: &no}).AdoptsAddress() {
		t.Fatal("AdoptsAddress mismatch")
	}
	connected, commOK, overcurrent := (Bus{}).Values()
	if !connected || !commOK || overcurrent {
		t.Fatalf("default bus = %v/%v/%v", connected, commOK, overcurrent)
	}
	connected, commOK, overcurrent = (Bus{Connected: &no, CommOK: &no, Overcurrent: &yes}).Values()
	if connected || commOK || !overcurrent {
		t.Fatalf("explicit bus = %v/%v/%v", connected, commOK, overcurrent)
	}
}

func TestDefaultPathsAndSeed(t *testing.T) {
	if DefaultBaseDir() == "" {
		t.Fatal("DefaultBaseDir is empty")
	}
	if got, want := DefaultConfigPath("base"), filepath.Join("base", "config", "config.yaml"); got != want {
		t.Fatalf("DefaultConfigPath = %q want %q", got, want)
	}
	if got, want := DefaultStatePath("base"), filepath.Join("base", "data", "state.json"); got != want {
		t.Fatalf("DefaultStatePath = %q want %q", got, want)
	}
	seed := DefaultDeviceSeed(3, "UTC")
	if seed.ID != "iadu-3" || seed.Name != "机柜A 42U" || seed.TCPUnitID != 3 {
		t.Fatalf("seed identity = %+v", seed)
	}
	if seed.UCount != DefaultUCount || seed.Tags.Mode != "auto" || seed.Tags.Seed != "E200000000000000" {
		t.Fatalf("seed defaults = %+v", seed)
	}
	if seed.Climate.Timezone != "UTC" || !seed.Climate.IsEnabled() {
		t.Fatalf("seed climate = %+v", seed.Climate)
	}
	if !seed.AdoptsAddress() {
		t.Fatal("seed should adopt addresses")
	}
}

func TestLookupHelpers(t *testing.T) {
	c := Default(t.TempDir())
	if p, ok := c.PortByName("COM1"); !ok || p.Name != "COM1" {
		t.Fatalf("PortByName(COM1) = %+v, %v", p, ok)
	}
	if _, ok := c.PortByName("com1"); ok {
		t.Fatal("PortByName should be case-sensitive")
	}
	if d, ok := c.DeviceByID("iadu-1"); !ok || d.ID != "iadu-1" {
		t.Fatalf("DeviceByID = %+v, %v", d, ok)
	}
	if _, ok := c.DeviceByID("missing"); ok {
		t.Fatal("unexpected device lookup hit")
	}
	if got := c.BindingsFor("iadu-1"); len(got) != 2 {
		t.Fatalf("BindingsFor = %+v", got)
	}
	if got := c.BindingsFor("missing"); len(got) != 0 {
		t.Fatalf("BindingsFor(missing) = %+v", got)
	}
}

func TestLoadErrorsAndTokenOverride(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "missing.yaml"), dir); err == nil {
		t.Fatal("missing config should fail")
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("::: not yaml"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(bad, dir); err == nil {
		t.Fatal("malformed YAML should fail")
	}
	unknown := filepath.Join(dir, "unknown.yaml")
	if err := os.WriteFile(unknown, []byte("version: 1\nnope: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(unknown, dir); err == nil {
		t.Fatal("unknown fields should fail")
	}
	invalid := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(invalid, []byte(`
version: 1
devices:
  - {id: a, u_count: 99}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(invalid, dir); err == nil {
		t.Fatal("validation failure should be reported")
	}

	good := filepath.Join(dir, "good.yaml")
	if err := os.WriteFile(good, []byte(`
version: 1
server:
  web: {token: from-file}
devices: []
bindings: []
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IADU_SIM_TOKEN", "from-env")
	c, err := Load(good, dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Server.Web.Token != "from-env" {
		t.Fatalf("token = %q", c.Server.Web.Token)
	}
}

func TestValidatePortAndServerBranches(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"version", func(c *Config) { c.Version = 2 }},
		{"web listen", func(c *Config) { c.Server.Web.Listen = "bad" }},
		{"tcp listen", func(c *Config) { c.Server.ModbusTCP.Listen = "bad" }},
		{"default unit low", func(c *Config) { c.Server.ModbusTCP.DefaultUnit = 0 }},
		{"default unit high", func(c *Config) { c.Server.ModbusTCP.DefaultUnit = 248 }},
		{"log level", func(c *Config) { c.Server.Log.Level = "trace" }},
		{"server timezone", func(c *Config) { c.Server.Timezone = "Bad/Zone" }},
		{"empty state file", func(c *Config) { c.Server.StateFile = "  " }},
		{"port name", func(c *Config) { c.Ports[0].Name = "" }},
		{"duplicate port", func(c *Config) { c.Ports = append(c.Ports, c.Ports[0]) }},
		{"bad transport", func(c *Config) { c.Ports[0].Transport = "can" }},
		{"pty no link", func(c *Config) { c.Ports[0].Link = "" }},
		{"pty bad mode", func(c *Config) { c.Ports[0].Mode = "999" }},
		{"serial no device", func(c *Config) {
			c.Ports[0].Transport = "serial"
			c.Ports[0].Device = ""
		}},
		{"serial zero baud", func(c *Config) {
			c.Ports[0].Transport = "serial"
			c.Ports[0].Device = "/dev/ttyS0"
			c.Ports[0].Baud = 0
		}},
		{"rtu tcp listen", func(c *Config) {
			c.Ports[0].Transport = "rtu-tcp"
			c.Ports[0].RTUTCPListen = "nope"
		}},
		{"bcast reply", func(c *Config) { c.Ports[0].BcastReply = "many" }},
		{"broadcast mode", func(c *Config) { c.Ports[0].BroadcastMode = "some" }},
		{"negative delay", func(c *Config) { c.Ports[0].ResponseDelayMS = -1 }},
		{"huge delay", func(c *Config) { c.Ports[0].ResponseDelayMS = 60001 }},
		{"negative silence", func(c *Config) { c.Ports[0].TxSilenceMS = -1 }},
		{"huge silence", func(c *Config) { c.Ports[0].TxSilenceMS = 60001 }},
	}
	for _, tc := range cases {
		c := Default(t.TempDir())
		tc.mutate(c)
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}

	c := Default(t.TempDir())
	c.Server.ModbusTCP.Enabled = boolPtr(false)
	c.Server.ModbusTCP.Listen = "not checked"
	if err := c.Validate(); err != nil {
		t.Fatalf("disabled TCP listen should be ignored: %v", err)
	}
	c = Default(t.TempDir())
	c.Ports[0].Transport = "serial"
	c.Ports[0].Device = "/dev/ttyS0"
	if err := c.Validate(); err != nil {
		t.Fatalf("valid serial port rejected: %v", err)
	}
	c = Default(t.TempDir())
	c.Ports[0].Transport = "rtu-tcp"
	c.Ports[0].RTUTCPListen = "127.0.0.1:15021"
	if err := c.Validate(); err != nil {
		t.Fatalf("valid rtu-tcp port rejected: %v", err)
	}
}

func TestValidateDeviceAndBindingBranches(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"empty id", func(c *Config) { c.Devices[0].ID = " " }},
		{"duplicate id", func(c *Config) {
			c.Devices = append(c.Devices, c.Devices[0])
			c.Devices[1].TCPUnitID = 2
		}},
		{"zero u count", func(c *Config) { c.Devices[0].UCount = 0 }},
		{"large u count", func(c *Config) { c.Devices[0].UCount = 43 }},
		{"zero tcp unit", func(c *Config) { c.Devices[0].TCPUnitID = 0 }},
		{"large tcp unit", func(c *Config) { c.Devices[0].TCPUnitID = 248 }},
		{"duplicate tcp unit", func(c *Config) {
			c.Devices = append(c.Devices, c.Devices[0])
			c.Devices[1].ID = "other"
		}},
		{"long esn", func(c *Config) { c.Devices[0].ESN = strings.Repeat("x", 21) }},
		{"bad esn", func(c *Config) { c.Devices[0].ESN = "bad\n" }},
		{"bad occupancy", func(c *Config) { c.Devices[0].Occupancy = "0-3" }},
		{"changed hold zero", func(c *Config) { c.Devices[0].ChangedHoldMS = 0 }},
		{"changed hold high", func(c *Config) { c.Devices[0].ChangedHoldMS = 3600001 }},
		{"empty climate timezone", func(c *Config) { c.Devices[0].Climate.Timezone = "" }},
		{"bad climate timezone", func(c *Config) { c.Devices[0].Climate.Timezone = "Bad/Zone" }},
		{"climate update", func(c *Config) { c.Devices[0].Climate.UpdateMS = 99 }},
		{"climate scale zero", func(c *Config) { c.Devices[0].Climate.TimeScale = 0 }},
		{"climate scale high", func(c *Config) { c.Devices[0].Climate.TimeScale = 100001 }},
		{"fixed temp low", func(c *Config) { c.Devices[0].Fixed.Temp = -41 }},
		{"fixed temp high", func(c *Config) { c.Devices[0].Fixed.Temp = 88 }},
		{"fixed humi low", func(c *Config) { c.Devices[0].Fixed.Humi = -1 }},
		{"fixed humi high", func(c *Config) { c.Devices[0].Fixed.Humi = 101 }},
		{"binding device", func(c *Config) { c.Bindings[0].Device = "missing" }},
		{"binding port", func(c *Config) { c.Bindings[0].Port = "COM9" }},
		{"binding unit low", func(c *Config) { c.Bindings[0].UnitID = 0 }},
		{"binding unit high", func(c *Config) { c.Bindings[0].UnitID = 248 }},
	}
	for _, tc := range cases {
		c := Default(t.TempDir())
		tc.mutate(c)
		if err := c.Validate(); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}
}

func TestValidateTagsAndClimateBranches(t *testing.T) {
	tagCases := []struct {
		name   string
		mutate func(*Device)
	}{
		{"mode", func(d *Device) { d.Tags.Mode = "random" }},
		{"seed", func(d *Device) { d.Tags.Seed = "short" }},
		{"offset range", func(d *Device) { d.Tags.IndexOffset = 7; d.Tags.IndexBytes = 2 }},
		{"custom key", func(d *Device) { d.Tags.Custom = map[string]string{"0": "0000000000000000"} }},
		{"custom key text", func(d *Device) { d.Tags.Custom = map[string]string{"x": "0000000000000000"} }},
		{"custom tag", func(d *Device) { d.Tags.Custom = map[string]string{"1": "zz"} }},
	}
	for _, tc := range tagCases {
		c := Default(t.TempDir())
		tc.mutate(&c.Devices[0])
		if err := c.Validate(); err == nil {
			t.Fatalf("tags %s: expected validation error", tc.name)
		}
	}

	nan := math.NaN()
	climateCases := []struct {
		name   string
		mutate func(*Climate)
	}{
		{"temp NaN", func(cl *Climate) { cl.Temperature.Mean = nan }},
		{"temp amplitude NaN", func(cl *Climate) { cl.Temperature.Amplitude = nan }},
		{"temp peak NaN", func(cl *Climate) { cl.Temperature.PeakHour = nan }},
		{"temp harmonic NaN", func(cl *Climate) { cl.Temperature.Harmonic2Amplitude = nan }},
		{"temp min NaN", func(cl *Climate) { cl.Temperature.Min = nan }},
		{"temp max NaN", func(cl *Climate) { cl.Temperature.Max = nan }},
		{"temp reversed", func(cl *Climate) { cl.Temperature.Min = cl.Temperature.Max }},
		{"temp low", func(cl *Climate) { cl.Temperature.Min = -41 }},
		{"temp high", func(cl *Climate) { cl.Temperature.Max = 88 }},
		{"temp negative amplitude", func(cl *Climate) { cl.Temperature.Amplitude = -1 }},
		{"harmonic negative", func(cl *Climate) { cl.Temperature.Harmonic2Amplitude = -1 }},
		{"humi NaN", func(cl *Climate) { cl.Humidity.Mean = nan }},
		{"humi coupling NaN", func(cl *Climate) { cl.Humidity.TempCoupling = nan }},
		{"humi amplitude NaN", func(cl *Climate) { cl.Humidity.Amplitude = nan }},
		{"humi min NaN", func(cl *Climate) { cl.Humidity.Min = nan }},
		{"humi max NaN", func(cl *Climate) { cl.Humidity.Max = nan }},
		{"humi reversed", func(cl *Climate) { cl.Humidity.Min = cl.Humidity.Max }},
		{"humi low", func(cl *Climate) { cl.Humidity.Min = -1 }},
		{"humi high", func(cl *Climate) { cl.Humidity.Max = 101 }},
		{"humi negative amplitude", func(cl *Climate) { cl.Humidity.Amplitude = -1 }},
		{"zone temp NaN", func(cl *Climate) { cl.ZoneOffsets.Top.Temp = nan }},
		{"zone humi NaN", func(cl *Climate) { cl.ZoneOffsets.Mid.Humi = nan }},
		{"zone lag NaN", func(cl *Climate) { cl.ZoneOffsets.Bottom.LagH = nan }},
		{"noise temp tau", func(cl *Climate) { cl.Noise.TempTauMS = 0 }},
		{"noise humi tau", func(cl *Climate) { cl.Noise.HumiTauMS = 0 }},
		{"noise temp sigma NaN", func(cl *Climate) { cl.Noise.TempSigma = nan }},
		{"noise humi sigma NaN", func(cl *Climate) { cl.Noise.HumiSigma = nan }},
		{"noise temp sigma negative", func(cl *Climate) { cl.Noise.TempSigma = -1 }},
		{"noise humi sigma negative", func(cl *Climate) { cl.Noise.HumiSigma = -1 }},
	}
	for _, tc := range climateCases {
		c := Default(t.TempDir())
		tc.mutate(&c.Devices[0].Climate)
		if err := c.Validate(); err == nil {
			t.Fatalf("climate %s: expected validation error", tc.name)
		}
	}
	for _, bad := range []string{"127.0.0.1", "127.0.0.1:0", "127.0.0.1:70000", "127.0.0.1:x"} {
		if err := validateListen(bad); err == nil {
			t.Fatalf("validateListen(%q) should fail", bad)
		}
	}
	if err := validateListen("0.0.0.0:15020"); err != nil {
		t.Fatalf("valid listen rejected: %v", err)
	}
}

func TestAbsentVersusExplicitEmptyDevices(t *testing.T) {
	dir := t.TempDir()
	absent := filepath.Join(dir, "absent.yaml")
	if err := os.WriteFile(absent, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(absent, dir)
	if err != nil {
		t.Fatalf("Load absent: %v", err)
	}
	if len(c.Devices) != 1 || len(c.Bindings) != 2 || len(c.Ports) != 2 {
		t.Fatalf("absent defaults = devices %d bindings %d ports %d",
			len(c.Devices), len(c.Bindings), len(c.Ports))
	}
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(empty, []byte("version: 1\ndevices: []\nbindings: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = Load(empty, dir)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(c.Devices) != 0 || len(c.Bindings) != 0 {
		t.Fatalf("explicit empty = devices %d bindings %d", len(c.Devices), len(c.Bindings))
	}
}

func TestBitsAndModeHelpers(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want os.FileMode
	}{
		{"0666", 0o666},
		{"0o660", 0o660},
		{"0640", 0o640},
	} {
		got, err := ParseFileMode(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseFileMode(%q) = %v, %v", tc.in, got, err)
		}
	}
	for _, bad := range []string{"888", "0x10", "-1", "1000"} {
		if _, err := ParseFileMode(bad); err == nil {
			t.Fatalf("ParseFileMode(%q) should fail", bad)
		}
	}
	if _, err := ParseOccupancy("all", 0); err == nil {
		t.Fatal("u_count 0 should fail")
	}
	if _, err := ParseOccupancy("all", 65); err == nil {
		t.Fatal("u_count 65 should fail")
	}
	mask, err := ParseOccupancy("1-3,5", 5)
	if err != nil || mask != 0b10111 {
		t.Fatalf("ParseOccupancy = %#x, %v", mask, err)
	}
	if got := FormatOccupancy(0b10101, 5); got != "1,3,5" {
		t.Fatalf("FormatOccupancy = %q", got)
	}
	if got := FormatOccupancy(0b100000, 5); got != "none" {
		t.Fatalf("FormatOccupancy out-of-range bits = %q", got)
	}
	if got := FormatOccupancy(1, 0); got != "none" {
		t.Fatalf("FormatOccupancy zero count = %q", got)
	}
	tag, err := ParseTagHex("0xE200000000000001")
	if err != nil || FormatTagHex(tag) != "E200000000000001" {
		t.Fatalf("tag 0x prefix = %s, %v", FormatTagHex(tag), err)
	}
	if esn := NormalizeESN("123456789012345678901234"); string(esn[:]) != "12345678901234567890" {
		t.Fatalf("ESN should truncate to 20 bytes: %q", string(esn[:]))
	}
	if seed, err := ParseSeed("E200000000000000"); err != nil || seed[0] != 0xE2 {
		t.Fatalf("ParseSeed = % X, %v", seed, err)
	}
}

func TestAssignTCPUnitsExhaustion(t *testing.T) {
	c := &Config{}
	for i := 1; i <= 247; i++ {
		c.Devices = append(c.Devices, Device{TCPUnitID: uint8(i)})
	}
	c.Devices = append(c.Devices, Device{})
	c.assignTCPUnits()
	if got := c.Devices[247].TCPUnitID; got != 0 {
		t.Fatalf("exhausted assignment = %d", got)
	}
}

func TestWriteDefaultErrors(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteDefault(filepath.Join(blocker, "config.yaml"), dir); err == nil {
		t.Fatal("WriteDefault under a file should fail")
	}
}
