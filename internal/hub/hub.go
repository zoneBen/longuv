// Package hub owns the runtime devices, port bindings and statistics shared by
// the RTU transports, the Modbus TCP adjustment server and the web console.
package hub

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/model"
	"github.com/zoneBen/longuv/internal/state"
)

// PortRuntime holds the mutable statistics of one configured port.
type PortRuntime struct {
	Config config.Port

	mu        sync.RWMutex
	slavePath string

	Connected atomic.Bool
	RX        atomic.Uint64
	TX        atomic.Uint64
	RXBytes   atomic.Uint64
	TXBytes   atomic.Uint64
	CRCErrors atomic.Uint64
	LastRX    atomic.Int64
	LastTX    atomic.Int64
}

// SetSlavePath records the allocated pty slave path.
func (p *PortRuntime) SetSlavePath(path string) {
	p.mu.Lock()
	p.slavePath = path
	p.mu.Unlock()
}

// SlavePath returns the allocated pty slave path.
func (p *PortRuntime) SlavePath() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.slavePath
}

// PortInfo is an immutable snapshot of a port for the API.
type PortInfo struct {
	Name      string     `json:"name"`
	Transport string     `json:"transport"`
	Link      string     `json:"link,omitempty"`
	Device    string     `json:"device,omitempty"`
	SlavePath string     `json:"slave_path,omitempty"`
	Connected bool       `json:"connected"`
	RX        uint64     `json:"rx"`
	TX        uint64     `json:"tx"`
	RXBytes   uint64     `json:"rx_bytes"`
	TXBytes   uint64     `json:"tx_bytes"`
	CRCErrors uint64     `json:"crc_errors"`
	LastRX    *time.Time `json:"last_rx,omitempty"`
	LastTX    *time.Time `json:"last_tx,omitempty"`
}

// Hub is the central runtime state.
type Hub struct {
	mu          sync.RWMutex
	devices     map[string]*model.Device
	deviceOrder []string
	bindings    []model.Binding
	ports       map[string]*PortRuntime
	portOrder   []string
	ring        *framelog.Ring
	log         *slog.Logger
	onChange    func()
	defaultUnit uint8
	started     time.Time
}

// New builds a hub from the validated configuration (first-run seed).
func New(cfg *config.Config, ring *framelog.Ring, log *slog.Logger, onChange func()) (*Hub, error) {
	if cfg == nil {
		return nil, fmt.Errorf("配置为空")
	}
	snaps := make([]model.Snapshot, 0, len(cfg.Devices))
	for _, seed := range cfg.Devices {
		snap, err := SnapshotFromConfig(seed)
		if err != nil {
			return nil, err
		}
		snaps = append(snaps, snap)
	}
	binds := make([]model.Binding, 0, len(cfg.Bindings))
	for _, b := range cfg.Bindings {
		binds = append(binds, model.Binding{
			DeviceID: b.Device,
			Port:     b.Port,
			UnitID:   b.UnitID,
		})
	}
	return newHub(cfg, snaps, binds, ring, log, onChange)
}

// NewFromState builds a hub from a persisted state file.
func NewFromState(cfg *config.Config, st state.File, ring *framelog.Ring, log *slog.Logger, onChange func()) (*Hub, error) {
	if st.Version != state.CurrentVersion {
		return nil, fmt.Errorf("状态版本 %d 不受支持", st.Version)
	}
	return newHub(cfg, st.Devices, st.Bindings, ring, log, onChange)
}

func newHub(cfg *config.Config, snaps []model.Snapshot, binds []model.Binding, ring *framelog.Ring, log *slog.Logger, onChange func()) (*Hub, error) {
	if cfg == nil {
		return nil, fmt.Errorf("配置为空")
	}
	if log == nil {
		log = slog.Default()
	}
	if ring == nil {
		ring = framelog.New(2048)
	}
	h := &Hub{
		devices:     make(map[string]*model.Device, len(snaps)),
		ports:       make(map[string]*PortRuntime, len(cfg.Ports)),
		ring:        ring,
		log:         log,
		onChange:    onChange,
		defaultUnit: cfg.Server.ModbusTCP.DefaultUnit,
		started:     time.Now(),
	}
	for _, p := range cfg.Ports {
		if _, dup := h.ports[p.Name]; dup {
			return nil, fmt.Errorf("端口 %q 重复", p.Name)
		}
		h.ports[p.Name] = &PortRuntime{Config: p}
		h.portOrder = append(h.portOrder, p.Name)
	}
	now := time.Now()
	for _, snap := range snaps {
		dev, err := model.New(snap, now, h.markDirty)
		if err != nil {
			return nil, err
		}
		if _, dup := h.devices[dev.ID()]; dup {
			return nil, fmt.Errorf("设备 id %q 重复", dev.ID())
		}
		if dev.TCPUnitID() < 1 || dev.TCPUnitID() > 247 {
			return nil, fmt.Errorf("设备 %s 的 tcp_unit_id %d 非法", dev.ID(), dev.TCPUnitID())
		}
		for _, other := range h.devices {
			if other.TCPUnitID() == dev.TCPUnitID() {
				return nil, fmt.Errorf("设备 %s 与 %s 的 tcp_unit_id 都是 %d",
					dev.ID(), other.ID(), dev.TCPUnitID())
			}
		}
		h.devices[dev.ID()] = dev
		h.deviceOrder = append(h.deviceOrder, dev.ID())
	}
	for _, b := range binds {
		if _, ok := h.devices[b.DeviceID]; !ok {
			return nil, fmt.Errorf("绑定引用了不存在的设备 %q", b.DeviceID)
		}
		if _, ok := h.ports[b.Port]; !ok {
			return nil, fmt.Errorf("绑定引用了不存在的端口 %q", b.Port)
		}
		if b.UnitID < 1 || b.UnitID > 247 {
			return nil, fmt.Errorf("端口 %s 的绑定地址 %d 非法", b.Port, b.UnitID)
		}
		for _, existing := range h.bindings {
			if existing.Port == b.Port && existing.UnitID == b.UnitID {
				return nil, fmt.Errorf("端口 %s 的地址 %d 被重复绑定", b.Port, b.UnitID)
			}
		}
		h.bindings = append(h.bindings, b)
	}
	h.updatePrimaryAddressesLocked()
	return h, nil
}

// SnapshotFile captures the runtime state for persistence.
func (h *Hub) SnapshotFile() state.File {
	h.mu.RLock()
	devices := make([]model.Snapshot, 0, len(h.deviceOrder))
	for _, id := range h.deviceOrder {
		if d := h.devices[id]; d != nil {
			devices = append(devices, d.Snapshot())
		}
	}
	bindings := append([]model.Binding(nil), h.bindings...)
	h.mu.RUnlock()
	return state.File{
		Version:  state.CurrentVersion,
		SavedAt:  time.Now(),
		Devices:  devices,
		Bindings: bindings,
	}
}

// Ring returns the frame log ring.
func (h *Hub) Ring() *framelog.Ring { return h.ring }

// Logger returns the hub logger.
func (h *Hub) Logger() *slog.Logger { return h.log }

// Uptime returns the time since construction.
func (h *Hub) Uptime() time.Duration { return time.Since(h.started) }

// DefaultUnit returns the fallback unit id for the adjustment server.
func (h *Hub) DefaultUnit() uint8 { return h.defaultUnit }

// Device returns a device by id.
func (h *Hub) Device(id string) (*model.Device, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	d, ok := h.devices[id]
	return d, ok
}

// Devices returns all devices in insertion order.
func (h *Hub) Devices() []*model.Device {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*model.Device, 0, len(h.deviceOrder))
	for _, id := range h.deviceOrder {
		if d := h.devices[id]; d != nil {
			out = append(out, d)
		}
	}
	return out
}

// Bindings returns a copy of all bindings.
func (h *Hub) Bindings() []model.Binding {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]model.Binding(nil), h.bindings...)
}

// BindingFor looks up the binding of a port/unit pair.
func (h *Hub) BindingFor(port string, unit uint8) (model.Binding, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, b := range h.bindings {
		if b.Port == port && b.UnitID == unit {
			return b, true
		}
	}
	return model.Binding{}, false
}

// BindingsForPort returns the bindings on one port in insertion order.
func (h *Hub) BindingsForPort(port string) []model.Binding {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]model.Binding, 0, 2)
	for _, b := range h.bindings {
		if b.Port == port {
			out = append(out, b)
		}
	}
	return out
}

// DeviceByTCPUnit resolves a Modbus TCP unit id.
func (h *Hub) DeviceByTCPUnit(unit uint8) (*model.Device, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, d := range h.devices {
		if d.TCPUnitID() == unit {
			return d, true
		}
	}
	return nil, false
}

// Tick advances every device's climate simulation.
func (h *Hub) Tick(now time.Time) {
	for _, d := range h.Devices() {
		d.Tick(now)
	}
}

// PortRuntime returns the mutable runtime of one port.
func (h *Hub) PortRuntime(name string) (*PortRuntime, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.ports[name]
	return p, ok
}

// PortInfos returns an immutable snapshot of every port.
func (h *Hub) PortInfos() []PortInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]PortInfo, 0, len(h.portOrder))
	for _, name := range h.portOrder {
		p := h.ports[name]
		if p == nil {
			continue
		}
		info := PortInfo{
			Name:      p.Config.Name,
			Transport: p.Config.Transport,
			Link:      p.Config.Link,
			Device:    p.Config.Device,
			SlavePath: p.SlavePath(),
			Connected: p.Connected.Load(),
			RX:        p.RX.Load(),
			TX:        p.TX.Load(),
			RXBytes:   p.RXBytes.Load(),
			TXBytes:   p.TXBytes.Load(),
			CRCErrors: p.CRCErrors.Load(),
		}
		if ns := p.LastRX.Load(); ns > 0 {
			t := time.Unix(0, ns)
			info.LastRX = &t
		}
		if ns := p.LastTX.Load(); ns > 0 {
			t := time.Unix(0, ns)
			info.LastTX = &t
		}
		out = append(out, info)
	}
	return out
}

// SetConnected updates the connected indicator of a port.
func (h *Hub) SetConnected(port string, connected bool) {
	if p, ok := h.PortRuntime(port); ok {
		p.Connected.Store(connected)
	}
}

// SetSlavePath records the pty slave path of a port.
func (h *Hub) SetSlavePath(port, path string) {
	if p, ok := h.PortRuntime(port); ok {
		p.SetSlavePath(path)
	}
}

// RecordRX records one received frame.
func (h *Hub) RecordRX(port string, n int) {
	if p, ok := h.PortRuntime(port); ok {
		p.RX.Add(1)
		p.RXBytes.Add(uint64(n))
		p.LastRX.Store(time.Now().UnixNano())
	}
}

// RecordTX records one transmitted frame.
func (h *Hub) RecordTX(port string, n int) {
	if p, ok := h.PortRuntime(port); ok {
		p.TX.Add(1)
		p.TXBytes.Add(uint64(n))
		p.LastTX.Store(time.Now().UnixNano())
	}
}

// RecordCRCError records a CRC failure.
func (h *Hub) RecordCRCError(port string) {
	if p, ok := h.PortRuntime(port); ok {
		p.CRCErrors.Add(1)
	}
}

// AddDevice inserts a device at runtime.
func (h *Hub) AddDevice(snap model.Snapshot) error {
	if err := model.ValidateESN(snap.ESN); err != nil {
		return fmt.Errorf("设备 %s 的 esn: %w", snap.ID, err)
	}
	if snap.TCPUnitID < 1 || snap.TCPUnitID > 247 {
		return fmt.Errorf("tcp_unit_id 必须在 1..247")
	}
	dev, err := model.New(snap, time.Now(), h.markDirty)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, dup := h.devices[dev.ID()]; dup {
		return fmt.Errorf("设备 id %q 已存在", dev.ID())
	}
	for _, d := range h.devices {
		if d.TCPUnitID() == dev.TCPUnitID() {
			return fmt.Errorf("tcp_unit_id %d 已被设备 %s 使用", dev.TCPUnitID(), d.ID())
		}
	}
	h.devices[dev.ID()] = dev
	h.deviceOrder = append(h.deviceOrder, dev.ID())
	h.markDirtyLocked()
	return nil
}

// RemoveDevice removes a device and all of its bindings.
func (h *Hub) RemoveDevice(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.devices[id]; !ok {
		return fmt.Errorf("设备 %q 不存在", id)
	}
	delete(h.devices, id)
	for i, devID := range h.deviceOrder {
		if devID == id {
			h.deviceOrder = append(h.deviceOrder[:i], h.deviceOrder[i+1:]...)
			break
		}
	}
	kept := h.bindings[:0]
	for _, b := range h.bindings {
		if b.DeviceID != id {
			kept = append(kept, b)
		}
	}
	h.bindings = kept
	h.markDirtyLocked()
	return nil
}

// AddBinding attaches a device to a port.
func (h *Hub) AddBinding(b model.Binding) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.devices[b.DeviceID]; !ok {
		return fmt.Errorf("设备 %q 不存在", b.DeviceID)
	}
	if _, ok := h.ports[b.Port]; !ok {
		return fmt.Errorf("端口 %q 不存在", b.Port)
	}
	if b.UnitID < 1 || b.UnitID > 247 {
		return fmt.Errorf("unit_id 必须在 1..247")
	}
	for _, existing := range h.bindings {
		if existing.Port == b.Port && existing.UnitID == b.UnitID {
			return fmt.Errorf("端口 %s 的地址 %d 已被占用", b.Port, b.UnitID)
		}
	}
	h.bindings = append(h.bindings, b)
	h.updatePrimaryAddressesLocked()
	h.markDirtyLocked()
	return nil
}

// RemoveBinding removes one exact binding.
func (h *Hub) RemoveBinding(deviceID, port string, unit uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, b := range h.bindings {
		if b.DeviceID == deviceID && b.Port == port && b.UnitID == unit {
			h.bindings = append(h.bindings[:i], h.bindings[i+1:]...)
			h.updatePrimaryAddressesLocked()
			h.markDirtyLocked()
			return nil
		}
	}
	return fmt.Errorf("绑定 %s/%s/%d 不存在", deviceID, port, unit)
}

// ValidateTCPUnit checks that unit can be assigned to deviceID without
// changing anything.
func (h *Hub) ValidateTCPUnit(deviceID string, unit uint8) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.validateTCPUnitLocked(deviceID, unit)
}

func (h *Hub) validateTCPUnitLocked(deviceID string, unit uint8) error {
	if unit < 1 || unit > 247 {
		return fmt.Errorf("tcp_unit_id 必须在 1..247")
	}
	if _, ok := h.devices[deviceID]; !ok {
		return fmt.Errorf("设备 %q 不存在", deviceID)
	}
	for id, other := range h.devices {
		if id != deviceID && other.TCPUnitID() == unit {
			return fmt.Errorf("tcp_unit_id %d 已被设备 %s 使用", unit, id)
		}
	}
	return nil
}

// SetTCPUnit changes a device's adjustment-server unit id after checking that
// the new unit is not already in use.
func (h *Hub) SetTCPUnit(deviceID string, unit uint8) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.validateTCPUnitLocked(deviceID, unit); err != nil {
		return err
	}
	h.devices[deviceID].SetTCPUnit(unit)
	h.markDirtyLocked()
	return nil
}

// Rebind moves the binding on port from oldUnit to newUnit (address
// negotiation). It is called from the RTU frame path.
func (h *Hub) Rebind(port string, oldUnit, newUnit uint8) error {
	if newUnit < 1 || newUnit > 247 {
		return fmt.Errorf("unit_id 必须在 1..247")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if oldUnit == newUnit {
		return nil
	}
	for _, b := range h.bindings {
		if b.Port == port && b.UnitID == newUnit {
			return fmt.Errorf("端口 %s 的地址 %d 已被占用", port, newUnit)
		}
	}
	for i := range h.bindings {
		if h.bindings[i].Port == port && h.bindings[i].UnitID == oldUnit {
			h.bindings[i].UnitID = newUnit
			h.updatePrimaryAddressesLocked()
			h.markDirtyLocked()
			return nil
		}
	}
	return fmt.Errorf("端口 %s 的地址 %d 不存在", port, oldUnit)
}

// BindingsSorted returns a stable, human-friendly binding list.
func (h *Hub) BindingsSorted() []model.Binding {
	out := h.Bindings()
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].UnitID < out[j].UnitID
	})
	return out
}

func (h *Hub) updatePrimaryAddressesLocked() {
	for _, d := range h.devices {
		d.SetPrimaryAddress(0)
	}
	for _, b := range h.bindings {
		d := h.devices[b.DeviceID]
		if d == nil {
			continue
		}
		if d.PrimaryAddress() == 0 {
			d.SetPrimaryAddress(b.UnitID)
		}
	}
}

func (h *Hub) markDirty() {
	if h.onChange != nil {
		h.onChange()
	}
}

func (h *Hub) markDirtyLocked() {
	h.markDirty()
}
