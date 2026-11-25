package model

import (
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zoneBen/longuv/internal/climate"
)

// Register space limits mirror the Python reference implementation.
const (
	MaxRegisters = 0x6000

	ExcIllegalFunction = 0x01
	ExcIllegalAddress  = 0x02
	ExcIllegalValue    = 0x03
	ExcDeviceFailure   = 0x04
)

// Device is one simulated asset strip. It owns all register state and is
// shared by every port binding and by the Modbus TCP adjustment server.
type Device struct {
	mu sync.RWMutex

	id      string
	name    string
	uCount  int
	info    [6]uint16
	esn     [20]byte
	tags    [MaxUCount][8]byte
	present uint64
	changed uint64
	// changedAt records when each bit was pulsed; zero means expired.
	changedAt [MaxUCount]time.Time
	th        [climate.Zones]uint16
	bus       [3]uint16
	led       [MaxUCount]uint16

	hrx   map[uint16]uint16
	coils map[uint16]bool

	strict      bool
	adopt       bool
	changedHold time.Duration

	engine         *climate.Engine
	climateEnabled bool
	fixed          [2]float64
	tcpUnit        uint8

	onChange func()

	lastSeen atomic.Int64
	reads    atomic.Uint64
	writes   atomic.Uint64
}

// New constructs a device from a persisted snapshot. onChange is invoked
// whenever persisted state changes; it must be fast and must not call back
// into the device.
func New(snap Snapshot, now time.Time, onChange func()) (*Device, error) {
	if snap.UCount < 1 || snap.UCount > MaxUCount {
		return nil, fmt.Errorf("设备 %s 的 u_count 必须在 1..%d", snap.ID, MaxUCount)
	}
	if snap.ChangedHoldMS <= 0 {
		snap.ChangedHoldMS = 5000
	}
	name := snap.Name
	if name == "" {
		name = snap.ID
	}
	d := &Device{
		id:             snap.ID,
		name:           name,
		uCount:         snap.UCount,
		hrx:            make(map[uint16]uint16),
		coils:          make(map[uint16]bool),
		strict:         snap.Strict,
		adopt:          snap.AdoptAddress,
		changedHold:    time.Duration(snap.ChangedHoldMS) * time.Millisecond,
		climateEnabled: snap.Climate.Enabled,
		fixed:          snap.FixedTH,
		tcpUnit:        snap.TCPUnitID,
		onChange:       onChange,
	}
	for i := 0; i < snap.UCount; i++ {
		if i < len(snap.Tags) {
			tag, err := ParseTagHex(snap.Tags[i])
			if err != nil {
				return nil, fmt.Errorf("设备 %s 的 U%d 标签: %w", snap.ID, i+1, err)
			}
			d.tags[i] = tag
		}
		if i < len(snap.LED) {
			d.led[i] = snap.LED[i]
		}
	}
	d.present = snap.Present & MaskBits(snap.UCount)
	d.bus = snap.Bus
	d.esn = NormalizeESN(snap.ESN)
	d.info = [6]uint16{snap.Model, 0, snap.BaudCode, uint16(snap.UCount), snap.SoftVersion, snap.HardVersion}
	d.engine = climate.New(snap.Climate, seedForID(snap.ID), now)
	d.engine.SetManualStates(snap.Manual)
	d.engine.ExpireManual(now)
	d.engine.Refresh(now)
	d.refreshTHNoLock()
	d.lastSeen.Store(now.UnixNano())
	return d, nil
}

// ---------------------------------------------------------------- metadata

// ID returns the stable device id.
func (d *Device) ID() string { return d.id }

// Name returns the display name.
func (d *Device) Name() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.name
}

// UCount returns the configured number of U positions.
func (d *Device) UCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.uCount
}

// TCPUnitID returns the unit id used by the adjustment server.
func (d *Device) TCPUnitID() uint8 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tcpUnit
}

// ChangedHoldMS returns the changed-bit hold duration.
func (d *Device) ChangedHoldMS() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return int(d.changedHold / time.Millisecond)
}

// AdoptAddress reports whether address negotiation is enabled.
func (d *Device) AdoptAddress() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.adopt
}

// Strict reports whether undefined addresses raise exceptions.
func (d *Device) Strict() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.strict
}

// PrimaryAddress returns the address derived from the first binding.
func (d *Device) PrimaryAddress() uint8 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return uint8(d.info[1])
}

// ESNString returns the ESN without trailing padding spaces.
func (d *Device) ESNString() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return strings.TrimRight(string(d.esn[:]), " ")
}

// Stats is a point-in-time view of device activity.
type Stats struct {
	LastSeen time.Time
	Reads    uint64
	Writes   uint64
}

// Stats returns device activity counters.
func (d *Device) Stats() Stats {
	ns := d.lastSeen.Load()
	var seen time.Time
	if ns != 0 {
		seen = time.Unix(0, ns)
	}
	return Stats{LastSeen: seen, Reads: d.reads.Load(), Writes: d.writes.Load()}
}

// ---------------------------------------------------------------- mutations

// SetName changes the display name.
func (d *Device) SetName(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.name = strings.TrimSpace(name)
	d.dirtyLocked()
}

// SetESN replaces the 20-byte ESN. Values longer than 20 bytes or containing
// non-printable ASCII are rejected.
func (d *Device) SetESN(s string) error {
	if len(s) > 20 {
		return fmt.Errorf("ESN 最多 20 个 ASCII 字符")
	}
	for _, r := range s {
		if r < 32 || r > 126 {
			return fmt.Errorf("ESN 只能包含可打印 ASCII 字符")
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.esn = NormalizeESN(s)
	d.dirtyLocked()
	return nil
}

// SetInfoValues changes the readable identity registers.
func (d *Device) SetInfoValues(model, baudCode, softVersion, hardVersion uint16) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.info[0] = model
	d.info[2] = baudCode
	d.info[4] = softVersion
	d.info[5] = hardVersion
	d.dirtyLocked()
}

// SetPrimaryAddress updates the address register used by the TCP shadow view.
func (d *Device) SetPrimaryAddress(addr uint8) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.info[1] = uint16(addr)
}

// SetUCount resizes the U-position arrays.
func (d *Device) SetUCount(n int) error {
	if n < 1 || n > MaxUCount {
		return fmt.Errorf("U 位长度必须在 1..%d", MaxUCount)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if n < d.uCount {
		for i := n; i < d.uCount; i++ {
			d.tags[i] = [8]byte{}
			d.led[i] = 0
		}
		d.present &= MaskBits(n)
		d.changed &= MaskBits(n)
	}
	d.uCount = n
	d.info[3] = uint16(n)
	d.dirtyLocked()
	return nil
}

// SetTCPUnit changes the adjustment-server unit id.
func (d *Device) SetTCPUnit(unit uint8) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tcpUnit = unit
	d.dirtyLocked()
}

// SetAdoptAddress toggles bus address negotiation.
func (d *Device) SetAdoptAddress(v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.adopt = v
	d.dirtyLocked()
}

// SetStrict toggles undefined-address exceptions.
func (d *Device) SetStrict(v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.strict = v
	d.dirtyLocked()
}

// SetChangedHoldMS changes how long changed bits stay asserted.
func (d *Device) SetChangedHoldMS(ms int) error {
	if ms <= 0 || ms > 3600000 {
		return fmt.Errorf("changed_hold_ms 必须在 1..3600000")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.changedHold = time.Duration(ms) * time.Millisecond
	d.dirtyLocked()
	return nil
}

// SetBus updates the bus-status triad.
func (d *Device) SetBus(connected, commOK, overcurrent bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bus = [3]uint16{btoi(connected), btoi(commOK), btoi(overcurrent)}
	d.dirtyLocked()
}

// SetLED changes one LED value.
func (d *Device) SetLED(u int, value uint16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if u < 1 || u > d.uCount {
		return fmt.Errorf("U 位 %d 超出范围", u)
	}
	d.led[u-1] = value
	d.dirtyLocked()
	return nil
}

// SetPresent toggles a U position. Enabling generates a default tag when the
// current tag is zero; disabling clears the tag.
func (d *Device) SetPresent(u int, present bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if u < 1 || u > d.uCount {
		return fmt.Errorf("U 位 %d 超出范围", u)
	}
	d.setPresentLocked(u, present)
	d.dirtyLocked()
	return nil
}

// SetTag replaces the tag of one U position without changing presence.
func (d *Device) SetTag(u int, tag [8]byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if u < 1 || u > d.uCount {
		return fmt.Errorf("U 位 %d 超出范围", u)
	}
	d.tags[u-1] = tag
	d.dirtyLocked()
	return nil
}

// PulseChanged asserts the changed bit of one U position.
func (d *Device) PulseChanged(u int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if u < 1 || u > d.uCount {
		return fmt.Errorf("U 位 %d 超出范围", u)
	}
	d.pulseChangedLocked(u, time.Now())
	return nil
}

// PulseAllChanged asserts every changed bit.
func (d *Device) PulseAllChanged() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for i := 1; i <= d.uCount; i++ {
		d.pulseChangedLocked(i, now)
	}
}

// ClearChanged clears every changed bit.
func (d *Device) ClearChanged() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.changed = 0
}

// ChangedBits returns the currently asserted changed bits.
func (d *Device) ChangedBits() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.activeChangedLocked(time.Now())
}

// SetManual freezes one zone. A zero until keeps the override indefinitely.
func (d *Device) SetManual(zone climate.Zone, tempRaw, humiRaw uint8, until time.Time) {
	if zone < climate.Top || zone > climate.Bottom {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	w := d.engine.SetManual(zone, tempRaw, humiRaw, until)
	d.th[zone] = w
	d.dirtyLocked()
}

// ClearManual releases one zone back to automatic control.
func (d *Device) ClearManual(zone climate.Zone) {
	if zone < climate.Top || zone > climate.Bottom {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.engine.ClearManual(zone)
	d.engine.Refresh(time.Now())
	d.refreshTHNoLock()
	d.dirtyLocked()
}

// ClearAllManual releases every zone.
func (d *Device) ClearAllManual() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.engine.ClearAllManual()
	d.engine.Refresh(time.Now())
	d.refreshTHNoLock()
	d.dirtyLocked()
}

// SetClimateParams replaces the climate configuration.
func (d *Device) SetClimateParams(p climate.Params) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.engine.SetParams(p)
	d.climateEnabled = p.Enabled
	d.engine.Refresh(time.Now())
	d.refreshTHNoLock()
	d.dirtyLocked()
}

// RefreshClimate recomputes the climate immediately without persisting.
func (d *Device) RefreshClimate(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.engine.Refresh(now)
	d.refreshTHNoLock()
}

// ClimateParams returns a copy of the climate configuration.
func (d *Device) ClimateParams() climate.Params {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.engine.Params()
}

// ManualStates returns the manual override states.
func (d *Device) ManualStates() [climate.Zones]climate.ManualState {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.engine.ManualStates()
}

// ManualMask returns bit0=top, bit1=middle, bit2=bottom.
func (d *Device) ManualMask() uint8 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.engine.ManualMask()
}

// Tick advances climate and expires changed bits.
func (d *Device) Tick(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.climateEnabled {
		raw, changed := d.engine.Tick(now)
		if changed {
			d.th = raw
		}
	} else if d.engine.ExpireManual(now) {
		d.refreshTHNoLock()
	}
	d.clearExpiredChangedLocked(now)
}

// Snapshot returns a deep copy of the persisted device state.
func (d *Device) Snapshot() Snapshot {
	d.mu.RLock()
	defer d.mu.RUnlock()
	tags := make([]string, d.uCount)
	for i := 0; i < d.uCount; i++ {
		tags[i] = FormatTagHex(d.tags[i])
	}
	led := make([]uint16, d.uCount)
	copy(led, d.led[:d.uCount])
	return Snapshot{
		ID:            d.id,
		Name:          d.name,
		UCount:        d.uCount,
		Model:         d.info[0],
		BaudCode:      d.info[2],
		SoftVersion:   d.info[4],
		HardVersion:   d.info[5],
		ESN:           strings.TrimRight(string(d.esn[:]), " "),
		AdoptAddress:  d.adopt,
		ChangedHoldMS: int(d.changedHold / time.Millisecond),
		TCPUnitID:     d.tcpUnit,
		Strict:        d.strict,
		Tags:          tags,
		Present:       d.present,
		Bus:           d.bus,
		LED:           led,
		Climate:       d.engine.Params(),
		Manual:        d.engine.ManualStates(),
		FixedTH:       d.fixed,
	}
}

// ---------------------------------------------------------------- register reads

// ReadInput implements FC04.
func (d *Device) ReadInput(start, count uint16, unit uint8) ([]uint16, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if count == 0 || count > 125 || int(start)+int(count) > MaxRegisters {
		return nil, ExcIllegalAddress
	}
	out := make([]uint16, count)
	for i := 0; i < int(count); i++ {
		v, ok := d.inputAtLocked(start+uint16(i), unit)
		if !ok && d.strict {
			return nil, ExcIllegalAddress
		}
		out[i] = v
	}
	return out, 0
}

// ReadHolding implements FC03.
func (d *Device) ReadHolding(start, count uint16, unit uint8) ([]uint16, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if count == 0 || count > 125 || int(start)+int(count) > MaxRegisters {
		return nil, ExcIllegalAddress
	}
	out := make([]uint16, count)
	for i := 0; i < int(count); i++ {
		v, ok := d.holdingAtLocked(start+uint16(i), unit)
		if !ok && d.strict {
			return nil, ExcIllegalAddress
		}
		out[i] = v
	}
	return out, 0
}

// ReadCoils implements FC01.
func (d *Device) ReadCoils(start, count uint16) ([]bool, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if count == 0 || count > 125 || int(start)+int(count) > MaxRegisters {
		return nil, ExcIllegalAddress
	}
	out := make([]bool, count)
	for i := 0; i < int(count); i++ {
		v, ok := d.coils[start+uint16(i)]
		if !ok && d.strict {
			return nil, ExcIllegalAddress
		}
		out[i] = v
	}
	return out, 0
}

// ReadDiscrete implements FC02.
func (d *Device) ReadDiscrete(start, count uint16) ([]bool, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if count == 0 || count > 125 || int(start)+int(count) > MaxRegisters {
		return nil, ExcIllegalAddress
	}
	now := time.Now()
	out := make([]bool, count)
	for i := 0; i < int(count); i++ {
		v, ok := d.discreteAtLocked(start+uint16(i), now)
		if !ok && d.strict {
			return nil, ExcIllegalAddress
		}
		out[i] = v
	}
	return out, 0
}

// ---------------------------------------------------------------- register writes

// WriteHolding implements FC06.
func (d *Device) WriteHolding(addr, value uint16, _ uint8) uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeTouch()
	return d.writeHoldingLocked(addr, value)
}

// WriteHoldingMany implements FC10.
func (d *Device) WriteHoldingMany(start uint16, values []uint16, _ uint8) uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeTouch()
	if len(values) == 0 || int(start)+len(values) > MaxRegisters {
		return ExcIllegalAddress
	}
	for i, v := range values {
		if code := d.writeHoldingLocked(start+uint16(i), v); code != 0 {
			return code
		}
	}
	return 0
}

// WriteCoil implements FC05.
func (d *Device) WriteCoil(addr uint16, on bool) uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeTouch()
	if addr >= MaxRegisters {
		return ExcIllegalAddress
	}
	d.coils[addr] = on
	return 0
}

// WriteCoilsMany implements FC0F.
func (d *Device) WriteCoilsMany(start uint16, values []bool) uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeTouch()
	if len(values) == 0 || int(start)+len(values) > MaxRegisters {
		return ExcIllegalAddress
	}
	for i, v := range values {
		d.coils[start+uint16(i)] = v
	}
	return 0
}

// ---------------------------------------------------------------- internals

func (d *Device) writeHoldingLocked(addr, value uint16) uint8 {
	if addr >= MaxRegisters {
		return ExcIllegalAddress
	}
	switch {
	case addr <= 5:
		d.info[addr] = value
		d.dirtyLocked()
		return 0
	case addr >= 0x0100 && int(addr) < 0x0100+d.uCount:
		d.led[addr-0x0100] = value
		d.dirtyLocked()
		return 0
	default:
		if d.strict {
			return ExcIllegalAddress
		}
		d.hrx[addr] = value
		return 0
	}
}

func (d *Device) inputAtLocked(addr uint16, unit uint8) (uint16, bool) {
	if int(addr) < d.uCount*4 {
		u := int(addr) / 4
		k := int(addr) % 4
		return uint16(d.tags[u][2*k])<<8 | uint16(d.tags[u][2*k+1]), true
	}
	switch {
	case addr >= 0x0100 && addr <= 0x0102:
		return d.th[addr-0x0100], true
	case addr >= 0x0103 && addr <= 0x0105:
		return d.bus[addr-0x0103], true
	case addr >= 0x0150 && addr <= 0x0159:
		i := int(addr - 0x0150)
		return uint16(d.esn[2*i])<<8 | uint16(d.esn[2*i+1]), true
	}
	return 0, false
}

func (d *Device) holdingAtLocked(addr uint16, unit uint8) (uint16, bool) {
	switch {
	case addr <= 5:
		if addr == 1 && unit != 0 {
			return uint16(unit), true
		}
		return d.info[addr], true
	case addr >= 0x0100 && int(addr) < 0x0100+d.uCount:
		return d.led[addr-0x0100], true
	}
	if v, ok := d.hrx[addr]; ok {
		return v, true
	}
	return 0, false
}

func (d *Device) discreteAtLocked(addr uint16, now time.Time) (bool, bool) {
	if int(addr) < d.uCount {
		return d.present&(uint64(1)<<addr) != 0, true
	}
	if addr >= 0x0100 && int(addr) < 0x0100+d.uCount {
		i := int(addr - 0x0100)
		return d.changedActiveLocked(i, now), true
	}
	return false, false
}

func (d *Device) changedActiveLocked(i int, now time.Time) bool {
	if i < 0 || i >= d.uCount {
		return false
	}
	if d.changed&(uint64(1)<<uint(i)) == 0 {
		return false
	}
	return now.Sub(d.changedAt[i]) < d.changedHold
}

func (d *Device) activeChangedLocked(now time.Time) uint64 {
	var mask uint64
	for i := 0; i < d.uCount; i++ {
		if d.changedActiveLocked(i, now) {
			mask |= uint64(1) << uint(i)
		}
	}
	return mask
}

func (d *Device) clearExpiredChangedLocked(now time.Time) {
	for i := 0; i < d.uCount; i++ {
		bit := uint64(1) << uint(i)
		if d.changed&bit == 0 {
			continue
		}
		if now.Sub(d.changedAt[i]) >= d.changedHold {
			d.changed &^= bit
		}
	}
}

func (d *Device) pulseChangedLocked(u int, now time.Time) {
	bit := uint64(1) << uint(u-1)
	d.changed |= bit
	d.changedAt[u-1] = now
}

func (d *Device) setPresentLocked(u int, present bool) {
	bit := uint64(1) << uint(u-1)
	if present {
		d.present |= bit
		if d.tags[u-1] == ([8]byte{}) {
			d.tags[u-1] = DefaultTag(u)
		}
		return
	}
	d.present &^= bit
	d.tags[u-1] = [8]byte{}
}

func (d *Device) refreshTHNoLock() {
	if d.climateEnabled {
		d.th = d.engine.Current()
		return
	}
	man := d.engine.ManualStates()
	tr := climate.QuantTemp(d.fixed[0])
	hr := climate.QuantHumi(d.fixed[1])
	fixedWord := uint16(tr)<<8 | uint16(hr)
	for z := 0; z < int(climate.Zones); z++ {
		if man[z].Enabled {
			d.th[z] = uint16(man[z].TempRaw)<<8 | uint16(man[z].HumiRaw)
		} else {
			d.th[z] = fixedWord
		}
	}
}

func (d *Device) touch() {
	d.reads.Add(1)
	d.lastSeen.Store(time.Now().UnixNano())
}

func (d *Device) writeTouch() {
	d.writes.Add(1)
	d.lastSeen.Store(time.Now().UnixNano())
}

func (d *Device) dirtyLocked() {
	if d.onChange != nil {
		d.onChange()
	}
}

func seedForID(id string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return int64(h.Sum64())
}

func btoi(v bool) uint16 {
	if v {
		return 1
	}
	return 0
}
