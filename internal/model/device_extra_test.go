package model

import (
	"strings"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/climate"
)

func TestMetadataAccessorsAndMutation(t *testing.T) {
	dev := newTestDevice(t, nil)
	if dev.ID() != "test-dev" || dev.Name() != "test" || dev.UCount() != 42 {
		t.Fatalf("identity = %q/%q/%d", dev.ID(), dev.Name(), dev.UCount())
	}
	if dev.TCPUnitID() != 1 || dev.ChangedHoldMS() != 5000 {
		t.Fatalf("tcp unit/changed hold = %d/%d", dev.TCPUnitID(), dev.ChangedHoldMS())
	}
	if !dev.AdoptAddress() || dev.Strict() || dev.ClimateEnabled() {
		t.Fatalf("flags = adopt=%v strict=%v climate=%v",
			dev.AdoptAddress(), dev.Strict(), dev.ClimateEnabled())
	}
	if dev.PrimaryAddress() != 1 || dev.ESNString() != "TEST-ESN" {
		t.Fatalf("primary/esn = %d/%q", dev.PrimaryAddress(), dev.ESNString())
	}

	dev.SetName("  renamed  ")
	if dev.Name() != "renamed" {
		t.Fatalf("trimmed name = %q", dev.Name())
	}
	if err := dev.SetESN("NEW"); err != nil || dev.ESNString() != "NEW" {
		t.Fatalf("SetESN = %v, %q", err, dev.ESNString())
	}
	if err := dev.SetESN(strings.Repeat("x", 21)); err == nil {
		t.Fatal("21-byte ESN should be rejected")
	}
	if err := dev.SetESN("bad\n"); err == nil {
		t.Fatal("non-printable ESN should be rejected")
	}
	dev.SetInfoValues(0x9000, 4, 0x0203, 0x0304)
	words, _ := dev.ReadHolding(0, 6, 0)
	if words[0] != 0x9000 || words[2] != 4 || words[4] != 0x0203 || words[5] != 0x0304 {
		t.Fatalf("info values = %#v", words)
	}
	dev.SetTCPUnit(9)
	if dev.TCPUnitID() != 9 {
		t.Fatalf("tcp unit = %d", dev.TCPUnitID())
	}
	dev.SetAdoptAddress(false)
	dev.SetStrict(true)
	if dev.AdoptAddress() || !dev.Strict() {
		t.Fatal("flag setters did not apply")
	}
	if err := dev.SetChangedHoldMS(0); err == nil {
		t.Fatal("zero changed_hold_ms should be rejected")
	}
	if err := dev.SetChangedHoldMS(3600001); err == nil {
		t.Fatal("oversized changed_hold_ms should be rejected")
	}
	if err := dev.SetChangedHoldMS(1000); err != nil || dev.ChangedHoldMS() != 1000 {
		t.Fatalf("SetChangedHoldMS = %v, %d", err, dev.ChangedHoldMS())
	}
}

func TestUCountAndInvalidSetters(t *testing.T) {
	dev := newTestDevice(t, nil)
	if err := dev.SetUCount(0); err == nil {
		t.Fatal("u_count 0 should be rejected")
	}
	if err := dev.SetUCount(MaxUCount + 1); err == nil {
		t.Fatal("u_count above max should be rejected")
	}
	if err := dev.SetUCount(10); err != nil {
		t.Fatalf("SetUCount: %v", err)
	}
	if got := dev.Status().Present &^ MaskBits(10); got != 0 {
		t.Fatalf("present bits above the new count remain: %#x", got)
	}
	if err := dev.SetLED(0, 1); err == nil {
		t.Fatal("U0 LED should be rejected")
	}
	if err := dev.SetLED(11, 1); err == nil {
		t.Fatal("U11 LED should be rejected after resize")
	}
	if err := dev.SetPresent(11, true); err == nil {
		t.Fatal("U11 presence should be rejected after resize")
	}
	if err := dev.SetTag(11, [8]byte{}); err == nil {
		t.Fatal("U11 tag should be rejected after resize")
	}
	if err := dev.PulseChanged(11); err == nil {
		t.Fatal("U11 pulse should be rejected after resize")
	}
}

func TestPulseAllAndClearAllManual(t *testing.T) {
	dev := newTestDevice(t, nil)
	dev.PulseAllChanged()
	if got := dev.ChangedBits(); got != MaskBits(42) {
		t.Fatalf("pulse all bits = %#x", got)
	}
	dev.SetManual(climate.Top, 140, 120, time.Time{})
	dev.SetManual(climate.Bottom, 130, 110, time.Time{})
	dev.ClearAllManual()
	if dev.ManualMask() != 0 {
		t.Fatalf("manual mask after clear all = %d", dev.ManualMask())
	}
	// Invalid zones are ignored instead of panicking.
	dev.SetManual(climate.Zone(-1), 1, 2, time.Time{})
	dev.ClearManual(climate.Zone(99))
	if dev.ManualMask() != 0 {
		t.Fatal("invalid zone changed the manual state")
	}
}

func TestFixedTHValidationAndClimateParams(t *testing.T) {
	dev := newTestDevice(t, nil)
	if err := dev.SetFixedTH(-41, 50); err == nil {
		t.Fatal("temperature below -40 should be rejected")
	}
	if err := dev.SetFixedTH(25, 101); err == nil {
		t.Fatal("humidity above 100 should be rejected")
	}
	if err := dev.SetFixedTH(30, 60); err != nil {
		t.Fatalf("SetFixedTH: %v", err)
	}
	words, _ := dev.ReadInput(0x0100, 3, 1)
	want := uint16(climate.QuantTemp(30))<<8 | uint16(climate.QuantHumi(60))
	if words[0] != want || words[1] != want || words[2] != want {
		t.Fatalf("fixed TH = %#v want %#x", words, want)
	}

	params := climate.Params{
		Enabled: true, Timezone: "UTC", TimeScale: 1, UpdateMS: 5000,
		Temperature: climate.Temperature{Mean: 25, Amplitude: 1, PeakHour: 15, Min: 18, Max: 30},
		Humidity:    climate.Humidity{Mean: 50, TempCoupling: 1, Amplitude: 1, Min: 30, Max: 70},
		Noise:       climate.Noise{TempTauMS: 1000, HumiTauMS: 1000},
	}
	dev.SetClimateParams(params)
	if !dev.ClimateEnabled() {
		t.Fatal("climate should be enabled")
	}
	if got := dev.ClimateParams(); !got.Enabled || got.Temperature.Mean != 25 {
		t.Fatalf("climate params = %+v", got)
	}
	dev.RefreshClimate(time.Now().Add(time.Minute))
	if dev.ManualStates() != [climate.Zones]climate.ManualState{} {
		t.Fatal("unexpected manual states")
	}
	dev.SetManual(climate.Mid, 120, 100, time.Time{})
	if states := dev.ManualStates(); !states[climate.Mid].Enabled {
		t.Fatal("manual state not recorded")
	}
}

func TestStatusAndStats(t *testing.T) {
	dev := newTestDevice(t, nil)
	if st := dev.Stats(); !st.LastSeen.IsZero() || st.Reads != 0 || st.Writes != 0 {
		t.Fatalf("initial stats = %+v", st)
	}
	_, _ = dev.ReadInput(0, 4, 1)
	if code := dev.WriteHolding(0x0010, 1, 1); code != 0 {
		t.Fatalf("write code = %d", code)
	}
	if st := dev.Stats(); st.LastSeen.IsZero() || st.Reads != 1 || st.Writes != 1 {
		t.Fatalf("stats after IO = %+v", st)
	}
	status := dev.Status()
	if status.Reads != 1 || status.Writes != 1 || status.LastSeen.IsZero() {
		t.Fatalf("status = %+v", status)
	}
	if status.Present != testPresent() {
		t.Fatalf("status present = %#x", status.Present)
	}
}

func TestReadCoilsLenientAndStrict(t *testing.T) {
	dev := newTestDevice(t, nil)
	if _, code := dev.ReadCoils(0, 0); code != ExcIllegalAddress {
		t.Fatalf("zero coil count code = %d", code)
	}
	if _, code := dev.ReadCoils(0, 126); code != ExcIllegalAddress {
		t.Fatalf("oversized coil count code = %d", code)
	}
	if bits, code := dev.ReadCoils(0x1000, 3); code != 0 || len(bits) != 3 || bits[0] {
		t.Fatalf("lenient coil read = %#v code=%d", bits, code)
	}
	if _, code := dev.ReadDiscrete(0, 126); code != ExcIllegalAddress {
		t.Fatalf("oversized discrete count code = %d", code)
	}
	strict := newTestDevice(t, func(s *Snapshot) { s.Strict = true })
	if _, code := strict.ReadCoils(0x1000, 1); code != ExcIllegalAddress {
		t.Fatalf("strict coil read code = %d", code)
	}
	if _, code := strict.ReadDiscrete(0x1000, 1); code != ExcIllegalAddress {
		t.Fatalf("strict discrete read code = %d", code)
	}
}

func TestCoilWritesAndInvalidRanges(t *testing.T) {
	dev := newTestDevice(t, nil)
	if code := dev.WriteCoil(10, true); code != 0 {
		t.Fatalf("WriteCoil code = %d", code)
	}
	if bits, _ := dev.ReadCoils(10, 1); !bits[0] {
		t.Fatal("WriteCoil did not persist")
	}
	if code := dev.WriteCoil(MaxRegisters, true); code != ExcIllegalAddress {
		t.Fatalf("out-of-range WriteCoil code = %d", code)
	}
	if code := dev.WriteCoilsMany(20, []bool{true, false, true}); code != 0 {
		t.Fatalf("WriteCoilsMany code = %d", code)
	}
	if bits, _ := dev.ReadCoils(20, 3); !bits[0] || bits[1] || !bits[2] {
		t.Fatalf("WriteCoilsMany readback = %#v", bits)
	}
	if code := dev.WriteCoilsMany(20, nil); code != ExcIllegalAddress {
		t.Fatalf("empty WriteCoilsMany code = %d", code)
	}
	if code := dev.WriteCoilsMany(MaxRegisters-1, []bool{true, true}); code != ExcIllegalAddress {
		t.Fatalf("overflow WriteCoilsMany code = %d", code)
	}
	if code := dev.WriteHoldingMany(MaxRegisters-1, []uint16{1, 2}, 1); code != ExcIllegalAddress {
		t.Fatalf("overflow WriteHoldingMany code = %d", code)
	}
	if code := dev.WriteHoldingMany(0x100, nil, 1); code != ExcIllegalAddress {
		t.Fatalf("empty WriteHoldingMany code = %d", code)
	}
}

func TestShadowInputCoilsAndDiscrete(t *testing.T) {
	dev := newTestDevice(t, nil)
	words, code := dev.ReadShadowInput(ShadowBase, 6)
	if code != 0 || words[0] != 0x8020 || words[3] != 42 {
		t.Fatalf("shadow input = %#v code=%d", words, code)
	}
	dev.SetManual(climate.Top, 140, 120, time.Time{})
	if err := dev.PulseChanged(2); err != nil {
		t.Fatal(err)
	}
	coils, code := dev.ReadShadowCoils(ShadowBase, 0x66)
	if code != 0 {
		t.Fatalf("shadow coils code = %d", code)
	}
	if !coils[0] || !coils[19] || coils[10] {
		t.Fatalf("presence coils = %#v", coils[:20])
	}
	if !coils[0x30+1] || coils[0x30] {
		t.Fatalf("changed coils = %#v", coils[0x30:0x30+2])
	}
	if !coils[0x60] || coils[0x61] || coils[0x62] {
		t.Fatalf("manual coils = %#v", coils[0x60:0x63])
	}
	discrete, code := dev.ReadShadowDiscrete(ShadowBase, 0x66)
	if code != 0 {
		t.Fatalf("shadow discrete code = %d", code)
	}
	if !discrete[0x63] || !discrete[0x64] || discrete[0x65] {
		t.Fatalf("bus discrete = %#v", discrete[0x63:0x66])
	}
}

func TestShadowReadValidation(t *testing.T) {
	dev := newTestDevice(t, nil)
	cases := []struct {
		name string
		fn   func() uint8
	}{
		{"holding below base", func() uint8 { _, c := dev.ReadShadowHolding(ShadowBase-1, 1); return c }},
		{"holding zero count", func() uint8 { _, c := dev.ReadShadowHolding(ShadowBase, 0); return c }},
		{"holding oversized", func() uint8 { _, c := dev.ReadShadowHolding(ShadowBase, 126); return c }},
		{"holding overflow", func() uint8 { _, c := dev.ReadShadowHolding(0xFFFF, 2); return c }},
		{"input below base", func() uint8 { _, c := dev.ReadShadowInput(1, 1); return c }},
		{"coils below base", func() uint8 { _, c := dev.ReadShadowCoils(1, 1); return c }},
		{"coils zero count", func() uint8 { _, c := dev.ReadShadowCoils(ShadowBase, 0); return c }},
		{"coils oversized", func() uint8 { _, c := dev.ReadShadowCoils(ShadowBase, 2001); return c }},
		{"discrete overflow", func() uint8 { _, c := dev.ReadShadowDiscrete(0xFFFF, 2); return c }},
	}
	for _, tc := range cases {
		if code := tc.fn(); code != ExcIllegalAddress {
			t.Fatalf("%s code = %d", tc.name, code)
		}
	}
}

func TestShadowWritesCoverAllRegions(t *testing.T) {
	dev := newTestDevice(t, nil)
	if code := dev.WriteShadowHolding(0x8006, 0x4142); code != 0 {
		t.Fatalf("ESN write code = %d", code)
	}
	if got := dev.ESNString(); !strings.HasPrefix(got, "AB") {
		t.Fatalf("ESN after shadow write = %q", got)
	}
	if code := dev.WriteShadowHolding(0x8101, 1); code != 0 {
		t.Fatalf("presence chunk write code = %d", code)
	}
	if bits, _ := dev.ReadDiscrete(16, 1); !bits[0] {
		t.Fatal("U17 presence not set")
	}
	if code := dev.WriteShadowHolding(0x8101, 0); code != 0 {
		t.Fatalf("presence chunk clear code = %d", code)
	}
	if bits, _ := dev.ReadDiscrete(16, 1); bits[0] {
		t.Fatal("U17 presence not cleared")
	}
	if code := dev.WriteShadowHolding(0x8102, 1); code != 0 {
		t.Fatalf("third presence chunk code = %d", code)
	}
	if bits, _ := dev.ReadDiscrete(32, 1); !bits[0] {
		t.Fatal("U33 presence not set")
	}
	if code := dev.WriteShadowHolding(0x8111, 1); code != 0 {
		t.Fatalf("changed chunk write code = %d", code)
	}
	if dev.ChangedBits()&(1<<16) == 0 {
		t.Fatal("U17 changed bit not pulsed")
	}
	if code := dev.WriteShadowHolding(0x8111, 0); code != 0 {
		t.Fatalf("changed chunk clear code = %d", code)
	}
	if dev.ChangedBits()&(1<<16) != 0 {
		t.Fatal("U17 changed bit not cleared")
	}
	if code := dev.WriteShadowHolding(0x8112, 1); code != 0 {
		t.Fatalf("third changed chunk code = %d", code)
	}
	if dev.ChangedBits()&(1<<32) == 0 {
		t.Fatal("U33 changed bit not pulsed")
	}
	if code := dev.WriteShadowHolding(0x8120, 3); code != 0 {
		t.Fatalf("unknown command code = %d", code)
	}
	if code := dev.WriteShadowHolding(0x80FA, 1); code != ExcIllegalAddress {
		t.Fatalf("LED beyond u_count code = %d", code)
	}
	if code := dev.WriteShadowHolding(0x80C6, 1); code != ExcIllegalAddress {
		t.Fatalf("reserved address code = %d", code)
	}
	if code := dev.WriteShadowHolding(0x7FFF, 1); code != ExcIllegalAddress {
		t.Fatalf("below shadow base code = %d", code)
	}
}

func TestShadowCoilWrites(t *testing.T) {
	dev := newTestDevice(t, nil)
	if code := dev.WriteShadowCoil(1, true); code != ExcIllegalAddress {
		t.Fatalf("below base coil code = %d", code)
	}
	if code := dev.WriteShadowCoil(ShadowBase+1, false); code != 0 {
		t.Fatalf("presence coil clear code = %d", code)
	}
	if bits, _ := dev.ReadDiscrete(1, 1); bits[0] {
		t.Fatal("U2 presence not cleared by coil")
	}
	if code := dev.WriteShadowCoil(ShadowBase+0x30+1, true); code != 0 {
		t.Fatalf("changed coil set code = %d", code)
	}
	if dev.ChangedBits()&(1<<1) == 0 {
		t.Fatal("U2 changed bit not set by coil")
	}
	if code := dev.WriteShadowCoil(ShadowBase+0x30+1, false); code != 0 {
		t.Fatalf("changed coil clear code = %d", code)
	}
	if dev.ChangedBits()&(1<<1) != 0 {
		t.Fatal("U2 changed bit not cleared by coil")
	}
	if code := dev.WriteShadowCoil(ShadowBase+0x70, false); code != 0 {
		t.Fatalf("clear coil false code = %d", code)
	}
	if code := dev.WriteShadowCoil(ShadowBase+0x70, true); code != 0 {
		t.Fatalf("clear coil true code = %d", code)
	}
	if code := dev.WriteShadowCoil(ShadowBase+0x5C, true); code != ExcIllegalAddress {
		t.Fatalf("reserved coil code = %d", code)
	}
}

func TestNewValidationAndDefaults(t *testing.T) {
	base := func() Snapshot {
		return Snapshot{
			ID:            "dev",
			UCount:        1,
			Tags:          []string{"0000000000000000"},
			Climate:       climate.Params{Timezone: "UTC", TimeScale: 1, UpdateMS: 5000},
			ChangedHoldMS: 0,
		}
	}
	if _, err := New(Snapshot{ID: "x", UCount: 0}, time.Now(), nil); err == nil {
		t.Fatal("u_count 0 should fail")
	}
	if _, err := New(Snapshot{ID: "x", UCount: MaxUCount + 1}, time.Now(), nil); err == nil {
		t.Fatal("u_count above max should fail")
	}
	badTag := base()
	badTag.Tags = []string{"short"}
	if _, err := New(badTag, time.Now(), nil); err == nil {
		t.Fatal("invalid tag should fail")
	}
	dev, err := New(base(), time.Now(), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if dev.Name() != "dev" {
		t.Fatalf("default name = %q", dev.Name())
	}
	if dev.ChangedHoldMS() != 5000 {
		t.Fatalf("default changed hold = %d", dev.ChangedHoldMS())
	}
}

func TestChangeCallbackFires(t *testing.T) {
	var calls int
	dev, err := New(Snapshot{
		ID: "cb", UCount: 1, Tags: []string{""},
		Climate: climate.Params{Timezone: "UTC", TimeScale: 1, UpdateMS: 5000},
	}, time.Now(), func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	dev.SetName("x")
	dev.SetLED(1, 1)
	dev.SetPresent(1, true)
	if calls < 3 {
		t.Fatalf("onChange calls = %d", calls)
	}
}

func TestTickExpiresManualWhenClimateDisabled(t *testing.T) {
	dev := newTestDevice(t, nil)
	dev.SetManual(climate.Top, 140, 120, time.Now().Add(5*time.Millisecond))
	if dev.ManualMask()&1 == 0 {
		t.Fatal("manual override missing")
	}
	time.Sleep(10 * time.Millisecond)
	dev.Tick(time.Now())
	if dev.ManualMask() != 0 {
		t.Fatalf("manual override did not expire: %d", dev.ManualMask())
	}
	words, _ := dev.ReadInput(0x0100, 1, 1)
	if words[0] != 0x8560 {
		t.Fatalf("TH after expiry = %#x", words[0])
	}
}

func TestSnapshotHelpers(t *testing.T) {
	if got := DefaultTag(0x1234); got != [8]byte{0xE2, 0, 0, 0, 0, 0, 0x12, 0x34} {
		t.Fatalf("DefaultTag = % X", got)
	}
	if MaskBits(0) != 0 || MaskBits(-1) != 0 {
		t.Fatal("MaskBits of non-positive should be zero")
	}
	if MaskBits(64) != ^uint64(0) || MaskBits(100) != ^uint64(0) {
		t.Fatal("MaskBits above 63 should be all ones")
	}
	if MaskBits(3) != 7 {
		t.Fatalf("MaskBits(3) = %#x", MaskBits(3))
	}
	if _, err := ParseTagHex("0xZZ00000000000000"); err == nil {
		t.Fatal("invalid hex should fail")
	}
	if got, err := ParseTagHex(" 0xE2 00 00 00 00 00 00 01 "); err != nil ||
		FormatTagHex(got) != "E200000000000001" {
		t.Fatalf("spaced/0x tag = %s, %v", FormatTagHex(got), err)
	}
	tags := GenerateTags(2, 0b11, [8]byte{0xE2}, 0, 8, map[int][8]byte{2: {9}})
	if tags[1] != [8]byte{9} {
		t.Fatalf("custom tag = % X", tags[1])
	}
	clamped := GenerateTags(1, 1, [8]byte{}, 6, 20, nil)
	if clamped[0] != [8]byte{} {
		t.Fatalf("clamped index bytes = % X", clamped[0])
	}
}
