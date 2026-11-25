package model

import (
	"time"

	"github.com/zoneBen/longuv/internal/climate"
)

// Shadow register map exposed by the Modbus TCP adjustment server. It starts
// at 0x8000, above the native register space (0x0000-0x5FFF), so the two
// address spaces can never collide.
const (
	ShadowBase = 0x8000

	shadowInfo     = ShadowBase + 0x000 // 0x8000-0x8005
	shadowESN      = ShadowBase + 0x006 // 0x8006-0x800F
	shadowTags     = ShadowBase + 0x010 // 0x8010-0x80B7
	shadowTH       = ShadowBase + 0x0C0 // 0x80C0-0x80C2
	shadowBus      = ShadowBase + 0x0C3 // 0x80C3-0x80C5
	shadowLED      = ShadowBase + 0x0D0 // 0x80D0-0x80F9
	shadowPresence = ShadowBase + 0x100 // 0x8100-0x8102
	shadowChanged  = ShadowBase + 0x110 // 0x8110-0x8112
	shadowCommand  = ShadowBase + 0x120 // 0x8120
	shadowMask     = ShadowBase + 0x121 // 0x8121

	shadowCoilPresence = ShadowBase + 0x000 // 0x8000-0x8029
	shadowCoilChanged  = ShadowBase + 0x030 // 0x8030-0x8059
	shadowCoilManual   = ShadowBase + 0x060 // 0x8060-0x8062
	shadowCoilClear    = ShadowBase + 0x070 // 0x8070
	shadowDIStatus     = ShadowBase + 0x063 // 0x8063-0x8065
)

// ReadShadowHolding reads the adjustment view (FC03).
func (d *Device) ReadShadowHolding(start, count uint16) ([]uint16, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if start < ShadowBase || count == 0 || count > 125 || int(start)+int(count) > 0x10000 {
		return nil, ExcIllegalAddress
	}
	now := time.Now()
	out := make([]uint16, count)
	for i := 0; i < int(count); i++ {
		out[i] = d.shadowReadAtLocked(start+uint16(i), now)
	}
	return out, 0
}

// ReadShadowInput reads the read-only mirror of the adjustment view (FC04).
func (d *Device) ReadShadowInput(start, count uint16) ([]uint16, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if start < ShadowBase || count == 0 || count > 125 || int(start)+int(count) > 0x10000 {
		return nil, ExcIllegalAddress
	}
	now := time.Now()
	out := make([]uint16, count)
	for i := 0; i < int(count); i++ {
		out[i] = d.shadowReadAtLocked(start+uint16(i), now)
	}
	return out, 0
}

// ReadShadowCoils reads the adjustment coil space (FC01).
func (d *Device) ReadShadowCoils(start, count uint16) ([]bool, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if start < ShadowBase || count == 0 || count > 2000 || int(start)+int(count) > 0x10000 {
		return nil, ExcIllegalAddress
	}
	now := time.Now()
	out := make([]bool, count)
	for i := 0; i < int(count); i++ {
		addr := start + uint16(i)
		switch {
		case addr >= shadowCoilPresence && int(addr) < shadowCoilPresence+d.uCount:
			out[i] = d.present&(uint64(1)<<uint(addr-shadowCoilPresence)) != 0
		case addr >= shadowCoilChanged && int(addr) < shadowCoilChanged+d.uCount:
			out[i] = d.changedActiveLocked(int(addr-shadowCoilChanged), now)
		case addr >= shadowCoilManual && addr < shadowCoilManual+climate.Zones:
			out[i] = d.engine.ManualMask()&(uint8(1)<<uint(addr-shadowCoilManual)) != 0
		}
	}
	return out, 0
}

// ReadShadowDiscrete reads the adjustment discrete-input view (FC02).
func (d *Device) ReadShadowDiscrete(start, count uint16) ([]bool, uint8) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	d.touch()
	if start < ShadowBase || count == 0 || count > 2000 || int(start)+int(count) > 0x10000 {
		return nil, ExcIllegalAddress
	}
	now := time.Now()
	out := make([]bool, count)
	for i := 0; i < int(count); i++ {
		addr := start + uint16(i)
		switch {
		case addr >= shadowCoilPresence && int(addr) < shadowCoilPresence+d.uCount:
			out[i] = d.present&(uint64(1)<<uint(addr-shadowCoilPresence)) != 0
		case addr >= shadowCoilChanged && int(addr) < shadowCoilChanged+d.uCount:
			out[i] = d.changedActiveLocked(int(addr-shadowCoilChanged), now)
		case addr >= shadowCoilManual && addr < shadowCoilManual+climate.Zones:
			out[i] = d.engine.ManualMask()&(uint8(1)<<uint(addr-shadowCoilManual)) != 0
		case addr >= shadowDIStatus && addr < shadowDIStatus+3:
			out[i] = d.bus[addr-shadowDIStatus] != 0
		}
	}
	return out, 0
}

// WriteShadowHolding writes one adjustment holding register (FC06/FC10).
func (d *Device) WriteShadowHolding(addr, value uint16) uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeTouch()
	if addr < ShadowBase {
		return ExcIllegalAddress
	}
	now := time.Now()
	switch {
	case addr >= shadowInfo && addr < shadowInfo+6:
		return ExcIllegalAddress // device identity is read-only
	case addr >= shadowESN && addr < shadowESN+10:
		hi, lo := byte(value>>8), byte(value)
		if hi < 32 || hi > 126 || lo < 32 || lo > 126 {
			return ExcIllegalValue
		}
		i := int(addr - shadowESN)
		d.esn[2*i] = hi
		d.esn[2*i+1] = lo
		d.dirtyLocked()
		return 0
	case addr >= shadowTags && int(addr) < shadowTags+d.uCount*4:
		u := int(addr-shadowTags) / 4
		k := int(addr-shadowTags) % 4
		d.tags[u][2*k] = byte(value >> 8)
		d.tags[u][2*k+1] = byte(value)
		d.dirtyLocked()
		return 0
	case addr >= shadowTH && addr < shadowTH+3:
		z := climate.Zone(addr - shadowTH)
		d.engine.SetManual(z, byte(value>>8), byte(value), time.Time{})
		d.th[z] = value
		d.dirtyLocked()
		return 0
	case addr >= shadowBus && addr < shadowBus+3:
		d.bus[addr-shadowBus] = btoi(value != 0)
		d.dirtyLocked()
		return 0
	case addr >= shadowLED && int(addr) < shadowLED+d.uCount:
		d.led[addr-shadowLED] = value
		d.dirtyLocked()
		return 0
	case addr >= shadowPresence && addr < shadowPresence+3:
		base := (int(addr - shadowPresence)) * 16
		for bit := 0; bit < 16; bit++ {
			u := base + bit + 1
			if u > d.uCount {
				break
			}
			d.setPresentLocked(u, value&(uint16(1)<<uint(bit)) != 0)
		}
		d.dirtyLocked()
		return 0
	case addr >= shadowChanged && addr < shadowChanged+3:
		base := (int(addr - shadowChanged)) * 16
		for bit := 0; bit < 16; bit++ {
			u := base + bit + 1
			if u > d.uCount {
				break
			}
			if value&(uint16(1)<<uint(bit)) != 0 {
				d.pulseChangedLocked(u, now)
			} else {
				d.changed &^= uint64(1) << uint(u-1)
			}
		}
		return 0
	case addr == shadowCommand:
		switch value {
		case 1:
			d.changed = 0
		case 2:
			d.engine.ClearAllManual()
			d.engine.Refresh(now)
			d.refreshTHNoLock()
			d.dirtyLocked()
		}
		return 0
	case addr == shadowMask:
		return ExcIllegalAddress
	default:
		return ExcIllegalAddress
	}
}

// WriteShadowCoil writes one adjustment coil (FC05/FC0F).
func (d *Device) WriteShadowCoil(addr uint16, on bool) uint8 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeTouch()
	if addr < ShadowBase {
		return ExcIllegalAddress
	}
	now := time.Now()
	switch {
	case addr >= shadowCoilPresence && int(addr) < shadowCoilPresence+d.uCount:
		d.setPresentLocked(int(addr-shadowCoilPresence)+1, on)
		d.dirtyLocked()
		return 0
	case addr >= shadowCoilChanged && int(addr) < shadowCoilChanged+d.uCount:
		u := int(addr-shadowCoilChanged) + 1
		if on {
			d.pulseChangedLocked(u, now)
		} else {
			d.changed &^= uint64(1) << uint(u-1)
		}
		return 0
	case addr >= shadowCoilManual && addr < shadowCoilManual+climate.Zones:
		z := climate.Zone(addr - shadowCoilManual)
		if on {
			w := d.th[z]
			d.engine.SetManual(z, byte(w>>8), byte(w), time.Time{})
			d.dirtyLocked()
		} else {
			d.engine.ClearManual(z)
			d.engine.Refresh(now)
			d.refreshTHNoLock()
			d.dirtyLocked()
		}
		return 0
	case addr == shadowCoilClear:
		if on {
			d.changed = 0
		}
		return 0
	default:
		return ExcIllegalAddress
	}
}

func (d *Device) shadowReadAtLocked(addr uint16, now time.Time) uint16 {
	switch {
	case addr >= shadowInfo && addr < shadowInfo+6:
		i := int(addr - shadowInfo)
		values := [6]uint16{d.info[0], d.info[1], d.info[2], uint16(d.uCount), d.info[4], d.info[5]}
		return values[i]
	case addr >= shadowESN && addr < shadowESN+10:
		i := int(addr - shadowESN)
		return uint16(d.esn[2*i])<<8 | uint16(d.esn[2*i+1])
	case addr >= shadowTags && int(addr) < shadowTags+d.uCount*4:
		u := int(addr-shadowTags) / 4
		k := int(addr-shadowTags) % 4
		return uint16(d.tags[u][2*k])<<8 | uint16(d.tags[u][2*k+1])
	case addr >= shadowTH && addr < shadowTH+3:
		return d.th[addr-shadowTH]
	case addr >= shadowBus && addr < shadowBus+3:
		return d.bus[addr-shadowBus]
	case addr >= shadowLED && int(addr) < shadowLED+d.uCount:
		return d.led[addr-shadowLED]
	case addr >= shadowPresence && addr < shadowPresence+3:
		shift := uint(addr-shadowPresence) * 16
		if shift >= 64 {
			return 0
		}
		return uint16(d.present >> shift)
	case addr >= shadowChanged && addr < shadowChanged+3:
		shift := uint(addr-shadowChanged) * 16
		if shift >= 64 {
			return 0
		}
		return uint16(d.activeChangedLocked(now) >> shift)
	case addr == shadowCommand:
		return 0
	case addr == shadowMask:
		return uint16(d.engine.ManualMask())
	default:
		return 0
	}
}
