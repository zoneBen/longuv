// Package config defines the YAML configuration for iadu-sim, its defaults
// and the persistence-free seed description of simulated devices.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// CurrentVersion is the only supported configuration schema version.
	CurrentVersion = 1
	// DefaultUCount matches the 42U iADU asset strip.
	DefaultUCount = 42
)

// Config is the complete process configuration.
type Config struct {
	Version  int       `yaml:"version"`
	Server   Server    `yaml:"server"`
	Ports    []Port    `yaml:"ports"`
	Devices  []Device  `yaml:"devices"`
	Bindings []Binding `yaml:"bindings"`
}

// Server groups process-wide settings.
type Server struct {
	Web       Web       `yaml:"web"`
	ModbusTCP ModbusTCP `yaml:"modbus_tcp"`
	Log       Log       `yaml:"log"`
	StateFile string    `yaml:"state_file"`
	Timezone  string    `yaml:"timezone"`
}

// Web configures the embedded management console and REST API.
type Web struct {
	Enabled       *bool  `yaml:"enabled"`
	Listen        string `yaml:"listen"`
	Token         string `yaml:"token"`
	SSEMaxClients int    `yaml:"sse_max_clients"`
	FrameBuffer   int    `yaml:"frame_buffer"`
}

// IsEnabled reports whether the web console should start.
func (w Web) IsEnabled() bool { return w.Enabled == nil || *w.Enabled }

// ModbusTCP configures the register-adjustment server (MBAP).
type ModbusTCP struct {
	Enabled      *bool  `yaml:"enabled"`
	Listen       string `yaml:"listen"`
	DefaultUnit  uint8  `yaml:"default_unit"`
	MaxConns     int    `yaml:"max_conns"`
	IdleTimeoutS int    `yaml:"idle_timeout_s"`
}

// IsEnabled reports whether the Modbus TCP server should start.
func (m ModbusTCP) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// Log configures file logging.
type Log struct {
	Level      string `yaml:"level"`
	File       string `yaml:"file"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
	JSON       bool   `yaml:"json"`
}

// Port describes one RTU transport endpoint.
type Port struct {
	Name            string `yaml:"name"`
	Transport       string `yaml:"transport"` // pty | serial | rtu-tcp
	Link            string `yaml:"link"`      // stable symlink path for pty
	Device          string `yaml:"device"`    // real serial device path
	Baud            int    `yaml:"baud"`
	Mode            string `yaml:"mode"`
	Group           string `yaml:"group"`
	ReplaceExisting bool   `yaml:"replace_existing"`
	ResponseDelayMS int    `yaml:"response_delay_ms"`
	BcastReply      string `yaml:"bcast_reply"`    // own | ff
	TxSilenceMS     int    `yaml:"tx_silence_ms"`  // half-duplex guard after writes
	BroadcastMode   string `yaml:"broadcast_mode"` // all | first
	RTUTCPListen    string `yaml:"rtu_tcp_listen"` // for transport=rtu-tcp
}

// Device is a seed description for one simulated asset strip.
type Device struct {
	ID            string  `yaml:"id"`
	Name          string  `yaml:"name"`
	UCount        int     `yaml:"u_count"`
	Model         uint16  `yaml:"model"`
	BaudCode      uint16  `yaml:"baud_code"`
	SoftVersion   uint16  `yaml:"soft_version"`
	HardVersion   uint16  `yaml:"hard_version"`
	ESN           string  `yaml:"esn"`
	AdoptAddress  *bool   `yaml:"adopt_address"`
	ChangedHoldMS int     `yaml:"changed_hold_ms"`
	TCPUnitID     uint8   `yaml:"tcp_unit_id"`
	Strict        bool    `yaml:"strict"`
	Tags          Tags    `yaml:"tags"`
	Occupancy     string  `yaml:"occupancy"`
	Bus           Bus     `yaml:"bus"`
	Climate       Climate `yaml:"climate"`
	Fixed         FixedTH `yaml:"fixed"`
}

// AdoptsAddress reports whether FC06 writes to register 0x0001 may change the
// bus address of this device.
func (d Device) AdoptsAddress() bool {
	if d.AdoptAddress == nil {
		return true
	}
	return *d.AdoptAddress
}

// Tags controls U-position tag generation.
type Tags struct {
	Mode        string            `yaml:"mode"` // auto | seed
	Seed        string            `yaml:"seed"`
	IndexOffset int               `yaml:"index_offset"`
	IndexBytes  int               `yaml:"index_bytes"`
	Custom      map[string]string `yaml:"custom"`
}

// Bus is the simulated bus-status triad. Nil booleans use healthy defaults.
type Bus struct {
	Connected   *bool `yaml:"connected"`
	CommOK      *bool `yaml:"comm_ok"`
	Overcurrent *bool `yaml:"overcurrent"`
}

// Values returns the effective bus status with healthy defaults applied.
func (b Bus) Values() (connected, commOK, overcurrent bool) {
	connected, commOK = true, true
	if b.Connected != nil {
		connected = *b.Connected
	}
	if b.CommOK != nil {
		commOK = *b.CommOK
	}
	if b.Overcurrent != nil {
		overcurrent = *b.Overcurrent
	}
	return connected, commOK, overcurrent
}

// Climate controls the dynamic temperature/humidity simulation.
type Climate struct {
	Enabled     *bool       `yaml:"enabled"`
	Timezone    string      `yaml:"timezone"`
	TimeScale   float64     `yaml:"time_scale"`
	UpdateMS    int         `yaml:"update_ms"`
	Temperature Temperature `yaml:"temperature"`
	Humidity    Humidity    `yaml:"humidity"`
	ZoneOffsets ZoneOffsets `yaml:"zone_offsets"`
	Noise       Noise       `yaml:"noise"`
}

// IsEnabled reports whether dynamic climate simulation is active.
func (c Climate) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// Temperature is the ambient daily curve.
type Temperature struct {
	Mean               float64 `yaml:"mean"`
	Amplitude          float64 `yaml:"amplitude"`
	PeakHour           float64 `yaml:"peak_hour"`
	Harmonic2Amplitude float64 `yaml:"harmonic2_amplitude"`
	Min                float64 `yaml:"min"`
	Max                float64 `yaml:"max"`
}

// Humidity is the humidity curve, coupled inversely to temperature.
type Humidity struct {
	Mean         float64 `yaml:"mean"`
	TempCoupling float64 `yaml:"temp_coupling"`
	Amplitude    float64 `yaml:"amplitude"`
	Min          float64 `yaml:"min"`
	Max          float64 `yaml:"max"`
}

// ZoneOffsets describes top/middle/bottom deviations.
type ZoneOffsets struct {
	Top    ZoneOffset `yaml:"top"`
	Mid    ZoneOffset `yaml:"mid"`
	Bottom ZoneOffset `yaml:"bottom"`
}

// ZoneOffset is the deviation and lag for one sensor zone.
type ZoneOffset struct {
	Temp float64 `yaml:"temp"`
	Humi float64 `yaml:"humi"`
	LagH float64 `yaml:"lag_h"`
}

// Noise configures the Ornstein-Uhlenbeck noise used by the climate engine.
type Noise struct {
	TempTauMS int     `yaml:"temp_tau_ms"`
	TempSigma float64 `yaml:"temp_sigma"`
	HumiTauMS int     `yaml:"humi_tau_ms"`
	HumiSigma float64 `yaml:"humi_sigma"`
}

// FixedTH is used when climate simulation is disabled.
type FixedTH struct {
	Temp float64 `yaml:"temp"`
	Humi float64 `yaml:"humi"`
}

// Binding attaches a device to a port at a Modbus unit id.
type Binding struct {
	Device string `yaml:"device"`
	Port   string `yaml:"port"`
	UnitID uint8  `yaml:"unit_id"`
}

// DefaultBaseDir returns the platform default for runtime data.
func DefaultBaseDir() string {
	if runtime.GOOS == "linux" {
		return "/opt/apps/iadu-sim"
	}
	return ".iadu-sim"
}

// DefaultConfigPath returns the default configuration path under baseDir.
func DefaultConfigPath(baseDir string) string {
	return filepath.Join(baseDir, "config", "config.yaml")
}

// DefaultStatePath returns the default state path under baseDir.
func DefaultStatePath(baseDir string) string {
	return filepath.Join(baseDir, "data", "state.json")
}

// Load reads, defaults and validates a YAML configuration.
func Load(path, baseDir string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s: %w", path, err)
	}
	if tok := os.Getenv("IADU_SIM_TOKEN"); tok != "" {
		c.Server.Web.Token = tok
	}
	c.applyDefaults(baseDir)
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败 (%s): %w", path, err)
	}
	return &c, nil
}

// DefaultDeviceSeed returns a fully populated device seed usable by the web
// API when creating a device at runtime.
func DefaultDeviceSeed(index int, timezone string) Device {
	d := defaultDevice(index, timezone)
	d.applyDefaults(index, timezone)
	return d
}

// Default returns a fully populated default configuration for baseDir.
func Default(baseDir string) *Config {
	if baseDir == "" {
		baseDir = DefaultBaseDir()
	}
	c := &Config{Version: CurrentVersion}
	c.Server = defaultServer(baseDir)
	c.Ports = defaultPorts(baseDir)
	c.Devices = []Device{defaultDevice(1, c.Server.Timezone)}
	c.Bindings = defaultBindings()
	return c
}

// GeneratedYAML renders the default configuration as YAML.
func GeneratedYAML(baseDir string) ([]byte, error) {
	c := Default(baseDir)
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("生成默认配置: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("生成默认配置: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteDefault writes a generated default configuration to path. It refuses
// to overwrite an existing file.
func WriteDefault(path, baseDir string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("配置文件已存在: %s", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查配置文件 %s: %w", path, err)
	}
	data, err := GeneratedYAML(baseDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("创建配置文件 %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("写入配置文件 %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭配置文件 %s: %w", path, err)
	}
	return nil
}

// PortByName returns a port by its (case-sensitive) name.
func (c *Config) PortByName(name string) (*Port, bool) {
	for i := range c.Ports {
		if c.Ports[i].Name == name {
			return &c.Ports[i], true
		}
	}
	return nil, false
}

// DeviceByID returns a device seed by id.
func (c *Config) DeviceByID(id string) (*Device, bool) {
	for i := range c.Devices {
		if c.Devices[i].ID == id {
			return &c.Devices[i], true
		}
	}
	return nil, false
}

// BindingsFor returns the bindings of one device.
func (c *Config) BindingsFor(deviceID string) []Binding {
	out := make([]Binding, 0, 2)
	for _, b := range c.Bindings {
		if b.Device == deviceID {
			out = append(out, b)
		}
	}
	return out
}

func (c *Config) applyDefaults(baseDir string) {
	if baseDir == "" {
		baseDir = DefaultBaseDir()
	}
	if c.Version == 0 {
		c.Version = CurrentVersion
	}

	// Server defaults.
	if c.Server.Web.Enabled == nil {
		c.Server.Web.Enabled = boolPtr(true)
	}
	if c.Server.Web.Listen == "" {
		c.Server.Web.Listen = "127.0.0.1:18090"
	}
	if c.Server.Web.SSEMaxClients <= 0 {
		c.Server.Web.SSEMaxClients = 64
	}
	if c.Server.Web.FrameBuffer <= 0 {
		c.Server.Web.FrameBuffer = 2048
	}
	if c.Server.ModbusTCP.Enabled == nil {
		c.Server.ModbusTCP.Enabled = boolPtr(true)
	}
	if c.Server.ModbusTCP.Listen == "" {
		c.Server.ModbusTCP.Listen = "0.0.0.0:15020"
	}
	if c.Server.ModbusTCP.DefaultUnit == 0 {
		c.Server.ModbusTCP.DefaultUnit = 1
	}
	if c.Server.ModbusTCP.MaxConns <= 0 {
		c.Server.ModbusTCP.MaxConns = 64
	}
	if c.Server.ModbusTCP.IdleTimeoutS <= 0 {
		c.Server.ModbusTCP.IdleTimeoutS = 120
	}
	if c.Server.Log.Level == "" {
		c.Server.Log.Level = "info"
	}
	if c.Server.Log.File == "" {
		c.Server.Log.File = filepath.Join(baseDir, "logs", "iadu-sim.log")
	}
	if c.Server.Log.MaxSizeMB <= 0 {
		c.Server.Log.MaxSizeMB = 20
	}
	if c.Server.Log.MaxBackups <= 0 {
		c.Server.Log.MaxBackups = 5
	}
	if c.Server.StateFile == "" {
		c.Server.StateFile = DefaultStatePath(baseDir)
	}
	if c.Server.Timezone == "" {
		c.Server.Timezone = "Asia/Shanghai"
	}

	// Ports.
	if c.Ports == nil {
		c.Ports = defaultPorts(baseDir)
	}
	for i := range c.Ports {
		p := &c.Ports[i]
		p.Name = strings.TrimSpace(p.Name)
		if p.Transport == "" {
			p.Transport = "pty"
		}
		if p.Baud == 0 {
			p.Baud = 9600
		}
		if p.Mode == "" {
			p.Mode = "0666"
		}
		if p.BcastReply == "" {
			p.BcastReply = "own"
		}
		if p.BroadcastMode == "" {
			p.BroadcastMode = "all"
		}
		if p.Transport == "pty" && p.Link == "" && p.Name != "" {
			p.Link = filepath.Join(baseDir, "run", strings.ToLower(p.Name))
		}
	}

	// Devices: an absent section means "use the default device", while an
	// explicit empty list leaves the simulator without devices.
	devicesAbsent := c.Devices == nil
	if devicesAbsent {
		c.Devices = []Device{defaultDevice(1, c.Server.Timezone)}
	}
	for i := range c.Devices {
		c.Devices[i].applyDefaults(i, c.Server.Timezone)
	}
	c.assignTCPUnits()

	if devicesAbsent && c.Bindings == nil {
		c.Bindings = defaultBindings()
	}
}

func (d *Device) applyDefaults(index int, defaultTZ string) {
	if d.ID == "" {
		d.ID = fmt.Sprintf("iadu-%d", index+1)
	}
	if d.Name == "" {
		d.Name = d.ID
	}
	if d.UCount == 0 {
		d.UCount = DefaultUCount
	}
	if d.Model == 0 {
		d.Model = 0x8020
	}
	if d.BaudCode == 0 {
		d.BaudCode = 3
	}
	if d.SoftVersion == 0 {
		d.SoftVersion = 0x0100
	}
	if d.HardVersion == 0 {
		d.HardVersion = 0x0100
	}
	if d.ESN == "" {
		d.ESN = fmt.Sprintf("E2%02X%016X", index+1, 0)
	}
	if d.AdoptAddress == nil {
		d.AdoptAddress = boolPtr(true)
	}
	if d.ChangedHoldMS == 0 {
		d.ChangedHoldMS = 5000
	}
	if d.Tags.Mode == "" {
		d.Tags.Mode = "auto"
	}
	if d.Tags.Mode == "auto" {
		if d.Tags.Seed == "" {
			d.Tags.Seed = "E200000000000000"
		}
		if d.Tags.IndexBytes == 0 {
			d.Tags.IndexOffset = 6
			d.Tags.IndexBytes = 2
		}
	}
	if d.Tags.Custom == nil {
		d.Tags.Custom = map[string]string{}
	}
	if strings.TrimSpace(d.Occupancy) == "" {
		d.Occupancy = "all"
	}
	if d.Bus.Connected == nil {
		d.Bus.Connected = boolPtr(true)
	}
	if d.Bus.CommOK == nil {
		d.Bus.CommOK = boolPtr(true)
	}
	if d.Bus.Overcurrent == nil {
		d.Bus.Overcurrent = boolPtr(false)
	}

	if d.Climate.Enabled == nil {
		d.Climate.Enabled = boolPtr(true)
	}
	if d.Climate.Timezone == "" {
		d.Climate.Timezone = defaultTZ
	}
	if d.Climate.TimeScale <= 0 {
		d.Climate.TimeScale = 1.0
	}
	if d.Climate.UpdateMS <= 0 {
		d.Climate.UpdateMS = 5000
	}
	def := defaultClimate(defaultTZ)
	if isZeroTemperature(d.Climate.Temperature) {
		d.Climate.Temperature = def.Temperature
	}
	if isZeroHumidity(d.Climate.Humidity) {
		d.Climate.Humidity = def.Humidity
	}
	if isZeroZoneOffsets(d.Climate.ZoneOffsets) {
		d.Climate.ZoneOffsets = def.ZoneOffsets
	}
	if d.Climate.Noise.TempTauMS <= 0 {
		d.Climate.Noise.TempTauMS = 180000
	}
	if d.Climate.Noise.HumiTauMS <= 0 {
		d.Climate.Noise.HumiTauMS = 180000
	}
	if d.Climate.Noise.TempSigma == 0 {
		d.Climate.Noise.TempSigma = 0.15
	}
	if d.Climate.Noise.HumiSigma == 0 {
		d.Climate.Noise.HumiSigma = 1.0
	}
	if d.Fixed.Temp == 0 && d.Fixed.Humi == 0 {
		d.Fixed = FixedTH{Temp: 25.0, Humi: 50.0}
	}
}

func (c *Config) assignTCPUnits() {
	used := make(map[uint8]bool, len(c.Devices))
	for _, d := range c.Devices {
		if d.TCPUnitID != 0 {
			used[d.TCPUnitID] = true
		}
	}
	for i := range c.Devices {
		if c.Devices[i].TCPUnitID != 0 {
			continue
		}
		for u := 1; u <= 247; u++ {
			if !used[uint8(u)] {
				c.Devices[i].TCPUnitID = uint8(u)
				used[uint8(u)] = true
				break
			}
		}
	}
}

func defaultServer(baseDir string) Server {
	return Server{
		Web: Web{
			Enabled:       boolPtr(true),
			Listen:        "127.0.0.1:18090",
			Token:         "",
			SSEMaxClients: 64,
			FrameBuffer:   2048,
		},
		ModbusTCP: ModbusTCP{
			Enabled:      boolPtr(true),
			Listen:       "0.0.0.0:15020",
			DefaultUnit:  1,
			MaxConns:     64,
			IdleTimeoutS: 120,
		},
		Log: Log{
			Level:      "info",
			File:       filepath.Join(baseDir, "logs", "iadu-sim.log"),
			MaxSizeMB:  20,
			MaxBackups: 5,
		},
		StateFile: DefaultStatePath(baseDir),
		Timezone:  "Asia/Shanghai",
	}
}

func defaultPorts(baseDir string) []Port {
	return []Port{
		{
			Name:          "COM1",
			Transport:     "pty",
			Link:          filepath.Join(baseDir, "run", "com1"),
			Baud:          9600,
			Mode:          "0666",
			BcastReply:    "own",
			BroadcastMode: "all",
		},
		{
			Name:          "COM2",
			Transport:     "pty",
			Link:          filepath.Join(baseDir, "run", "com2"),
			Baud:          9600,
			Mode:          "0666",
			BcastReply:    "own",
			BroadcastMode: "all",
		},
	}
}

func defaultDevice(index int, timezone string) Device {
	return Device{
		ID:            fmt.Sprintf("iadu-%d", index),
		Name:          "机柜A 42U",
		UCount:        DefaultUCount,
		Model:         0x8020,
		BaudCode:      3,
		SoftVersion:   0x0100,
		HardVersion:   0x0100,
		ESN:           fmt.Sprintf("E2%02X%016X", index, 0),
		AdoptAddress:  boolPtr(true),
		ChangedHoldMS: 5000,
		TCPUnitID:     uint8(index),
		Tags: Tags{
			Mode:        "auto",
			Seed:        "E200000000000000",
			IndexOffset: 6,
			IndexBytes:  2,
			Custom:      map[string]string{},
		},
		Occupancy: "1-10,20",
		Bus:       Bus{Connected: boolPtr(true), CommOK: boolPtr(true), Overcurrent: boolPtr(false)},
		Climate:   defaultClimate(timezone),
		Fixed:     FixedTH{Temp: 25.0, Humi: 50.0},
	}
}

func defaultBindings() []Binding {
	return []Binding{
		{Device: "iadu-1", Port: "COM1", UnitID: 1},
		{Device: "iadu-1", Port: "COM2", UnitID: 1},
	}
}

func defaultClimate(timezone string) Climate {
	return Climate{
		Enabled:   boolPtr(true),
		Timezone:  timezone,
		TimeScale: 1.0,
		UpdateMS:  5000,
		Temperature: Temperature{
			Mean:               24.5,
			Amplitude:          1.6,
			PeakHour:           15.0,
			Harmonic2Amplitude: 0.2,
			Min:                18.0,
			Max:                30.0,
		},
		Humidity: Humidity{
			Mean:         50.0,
			TempCoupling: 1.2,
			Amplitude:    1.5,
			Min:          30.0,
			Max:          70.0,
		},
		ZoneOffsets: ZoneOffsets{
			Top:    ZoneOffset{Temp: 0.8, Humi: -1.0, LagH: 0.25},
			Mid:    ZoneOffset{Temp: 0.0, Humi: 0.0, LagH: 0.0},
			Bottom: ZoneOffset{Temp: -0.6, Humi: 1.0, LagH: -0.25},
		},
		Noise: Noise{
			TempTauMS: 180000,
			TempSigma: 0.15,
			HumiTauMS: 180000,
			HumiSigma: 1.0,
		},
	}
}

func isZeroTemperature(t Temperature) bool {
	return t == Temperature{}
}

func isZeroHumidity(h Humidity) bool {
	return h == Humidity{}
}

func isZeroZoneOffsets(z ZoneOffsets) bool {
	return z == ZoneOffsets{}
}

func boolPtr(v bool) *bool { return &v }
