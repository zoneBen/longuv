package config

import (
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"time"
)

// Validate checks the complete configuration. It is intentionally strict:
// starting with a bad binding or a duplicate unit id silently corrupts the
// simulated bus, which is worse than refusing to start.
func (c *Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("version 必须为 %d，当前为 %d", CurrentVersion, c.Version)
	}
	if err := validateListen(c.Server.Web.Listen); err != nil {
		return fmt.Errorf("server.web.listen: %w", err)
	}
	if c.Server.ModbusTCP.IsEnabled() {
		if err := validateListen(c.Server.ModbusTCP.Listen); err != nil {
			return fmt.Errorf("server.modbus_tcp.listen: %w", err)
		}
	}
	if c.Server.ModbusTCP.DefaultUnit < 1 || c.Server.ModbusTCP.DefaultUnit > 247 {
		return fmt.Errorf("server.modbus_tcp.default_unit 必须在 1..247 之间")
	}
	switch strings.ToLower(c.Server.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("server.log.level 必须是 debug/info/warn/error 之一，得到 %q", c.Server.Log.Level)
	}
	if _, err := time.LoadLocation(c.Server.Timezone); err != nil {
		return fmt.Errorf("server.timezone %q 无法解析: %w", c.Server.Timezone, err)
	}
	if strings.TrimSpace(c.Server.StateFile) == "" {
		return fmt.Errorf("server.state_file 不能为空")
	}

	portNames := make(map[string]bool, len(c.Ports))
	for i := range c.Ports {
		p := &c.Ports[i]
		if p.Name == "" {
			return fmt.Errorf("ports[%d].name 不能为空", i)
		}
		if portNames[p.Name] {
			return fmt.Errorf("端口名 %q 重复", p.Name)
		}
		portNames[p.Name] = true
		switch p.Transport {
		case "pty":
			if strings.TrimSpace(p.Link) == "" {
				return fmt.Errorf("端口 %s 使用 pty 时必须配置 link", p.Name)
			}
			if _, err := ParseFileMode(p.Mode); err != nil {
				return fmt.Errorf("端口 %s 的 mode: %w", p.Name, err)
			}
		case "serial":
			if strings.TrimSpace(p.Device) == "" {
				return fmt.Errorf("端口 %s 使用 serial 时必须配置 device", p.Name)
			}
			if p.Baud <= 0 {
				return fmt.Errorf("端口 %s 的 baud 必须为正数", p.Name)
			}
		case "rtu-tcp":
			if err := validateListen(p.RTUTCPListen); err != nil {
				return fmt.Errorf("端口 %s 的 rtu_tcp_listen: %w", p.Name, err)
			}
		default:
			return fmt.Errorf("端口 %s 的 transport 必须是 pty/serial/rtu-tcp，得到 %q", p.Name, p.Transport)
		}
		if p.BcastReply != "own" && p.BcastReply != "ff" {
			return fmt.Errorf("端口 %s 的 bcast_reply 必须是 own 或 ff", p.Name)
		}
		if p.BroadcastMode != "all" && p.BroadcastMode != "first" {
			return fmt.Errorf("端口 %s 的 broadcast_mode 必须是 all 或 first", p.Name)
		}
		if p.ResponseDelayMS < 0 || p.ResponseDelayMS > 60000 {
			return fmt.Errorf("端口 %s 的 response_delay_ms 必须在 0..60000", p.Name)
		}
		if p.TxSilenceMS < 0 || p.TxSilenceMS > 60000 {
			return fmt.Errorf("端口 %s 的 tx_silence_ms 必须在 0..60000", p.Name)
		}
	}

	deviceIDs := make(map[string]bool, len(c.Devices))
	units := make(map[uint8]string, len(c.Devices))
	for i := range c.Devices {
		d := &c.Devices[i]
		if strings.TrimSpace(d.ID) == "" {
			return fmt.Errorf("devices[%d].id 不能为空", i)
		}
		if deviceIDs[d.ID] {
			return fmt.Errorf("设备 id %q 重复", d.ID)
		}
		deviceIDs[d.ID] = true
		if d.UCount < 1 || d.UCount > DefaultUCount {
			return fmt.Errorf("设备 %s 的 u_count 必须在 1..%d", d.ID, DefaultUCount)
		}
		if d.TCPUnitID < 1 || d.TCPUnitID > 247 {
			return fmt.Errorf("设备 %s 的 tcp_unit_id 必须在 1..247", d.ID)
		}
		if prev, ok := units[d.TCPUnitID]; ok {
			return fmt.Errorf("设备 %s 与 %s 的 tcp_unit_id 都是 %d", d.ID, prev, d.TCPUnitID)
		}
		units[d.TCPUnitID] = d.ID
		if err := validateESN(d.ESN); err != nil {
			return fmt.Errorf("设备 %s 的 esn: %w", d.ID, err)
		}
		if _, err := ParseOccupancy(d.Occupancy, d.UCount); err != nil {
			return fmt.Errorf("设备 %s 的 occupancy: %w", d.ID, err)
		}
		if err := validateTags(d); err != nil {
			return fmt.Errorf("设备 %s 的 tags: %w", d.ID, err)
		}
		if d.ChangedHoldMS <= 0 || d.ChangedHoldMS > 3600000 {
			return fmt.Errorf("设备 %s 的 changed_hold_ms 必须在 1..3600000", d.ID)
		}
		if d.Climate.Timezone == "" {
			return fmt.Errorf("设备 %s 的 climate.timezone 不能为空", d.ID)
		}
		if _, err := time.LoadLocation(d.Climate.Timezone); err != nil {
			return fmt.Errorf("设备 %s 的 climate.timezone %q 无法解析: %w", d.ID, d.Climate.Timezone, err)
		}
		if d.Climate.UpdateMS < 100 {
			return fmt.Errorf("设备 %s 的 climate.update_ms 不能小于 100", d.ID)
		}
		if d.Climate.TimeScale <= 0 || d.Climate.TimeScale > 100000 {
			return fmt.Errorf("设备 %s 的 climate.time_scale 必须在 (0,100000]", d.ID)
		}
		if err := validateClimate(d); err != nil {
			return fmt.Errorf("设备 %s 的 climate: %w", d.ID, err)
		}
		if d.Fixed.Temp < -40 || d.Fixed.Temp > 87.5 || d.Fixed.Humi < 0 || d.Fixed.Humi > 100 {
			return fmt.Errorf("设备 %s 的 fixed 温湿度超出范围", d.ID)
		}
	}

	seenBindings := make(map[string]bool, len(c.Bindings))
	for i, b := range c.Bindings {
		if !deviceIDs[b.Device] {
			return fmt.Errorf("bindings[%d] 引用了不存在的设备 %q", i, b.Device)
		}
		if !portNames[b.Port] {
			return fmt.Errorf("bindings[%d] 引用了不存在的端口 %q", i, b.Port)
		}
		if b.UnitID < 1 || b.UnitID > 247 {
			return fmt.Errorf("bindings[%d] 的 unit_id 必须在 1..247", i)
		}
		key := b.Port + "/" + strconv.Itoa(int(b.UnitID))
		if seenBindings[key] {
			return fmt.Errorf("端口 %s 上从站地址 %d 被重复绑定", b.Port, b.UnitID)
		}
		seenBindings[key] = true
	}
	return nil
}

func validateListen(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("监听地址不能为空")
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("监听地址 %q 应为 host:port: %w", addr, err)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("监听端口 %q 非法", port)
	}
	return nil
}

func validateESN(s string) error {
	if len(s) > 20 {
		return fmt.Errorf("ESN 最多 20 个 ASCII 字符，当前 %d", len(s))
	}
	for _, r := range s {
		if r < 32 || r > 126 {
			return fmt.Errorf("ESN 只能包含可打印 ASCII 字符")
		}
	}
	return nil
}

func validateTags(d *Device) error {
	if d.Tags.Mode != "auto" && d.Tags.Mode != "seed" {
		return fmt.Errorf("mode 必须是 auto 或 seed，得到 %q", d.Tags.Mode)
	}
	if _, err := ParseSeed(d.Tags.Seed); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	if d.Tags.IndexOffset < 0 || d.Tags.IndexBytes < 0 ||
		d.Tags.IndexOffset+d.Tags.IndexBytes > 8 {
		return fmt.Errorf("index_offset/index_bytes 超出 8 字节标签范围")
	}
	for key, raw := range d.Tags.Custom {
		u, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil || u < 1 || u > d.UCount {
			return fmt.Errorf("custom 键 %q 必须是 1..%d", key, d.UCount)
		}
		if _, err := ParseTagHex(raw); err != nil {
			return fmt.Errorf("custom[%s]: %w", key, err)
		}
	}
	return nil
}

func validateClimate(d *Device) error {
	t := d.Climate.Temperature
	if !finite(t.Mean) || !finite(t.Amplitude) || !finite(t.PeakHour) ||
		!finite(t.Harmonic2Amplitude) || !finite(t.Min) || !finite(t.Max) {
		return fmt.Errorf("temperature 含有非有限数值")
	}
	if t.Min >= t.Max {
		return fmt.Errorf("temperature.min 必须小于 max")
	}
	if t.Min < -40 || t.Max > 87.5 {
		return fmt.Errorf("temperature 范围必须在 -40..87.5℃ 内")
	}
	if t.Amplitude < 0 || t.Harmonic2Amplitude < 0 {
		return fmt.Errorf("temperature 振幅不能为负")
	}
	h := d.Climate.Humidity
	if !finite(h.Mean) || !finite(h.TempCoupling) || !finite(h.Amplitude) || !finite(h.Min) || !finite(h.Max) {
		return fmt.Errorf("humidity 含有非有限数值")
	}
	if h.Min >= h.Max {
		return fmt.Errorf("humidity.min 必须小于 max")
	}
	if h.Min < 0 || h.Max > 100 {
		return fmt.Errorf("humidity 范围必须在 0..100%% 内")
	}
	if h.Amplitude < 0 {
		return fmt.Errorf("humidity 振幅不能为负")
	}
	z := d.Climate.ZoneOffsets
	for name, off := range map[string]ZoneOffset{"top": z.Top, "mid": z.Mid, "bottom": z.Bottom} {
		if !finite(off.Temp) || !finite(off.Humi) || !finite(off.LagH) {
			return fmt.Errorf("zone_offsets.%s 含有非有限数值", name)
		}
	}
	n := d.Climate.Noise
	if n.TempTauMS <= 0 || n.HumiTauMS <= 0 {
		return fmt.Errorf("noise tau 必须为正数")
	}
	if !finite(n.TempSigma) || !finite(n.HumiSigma) || n.TempSigma < 0 || n.HumiSigma < 0 {
		return fmt.Errorf("noise sigma 必须是非负有限数")
	}
	return nil
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
