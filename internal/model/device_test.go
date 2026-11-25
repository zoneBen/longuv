package model

import (
	"reflect"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/climate"
)

func testPresent() uint64 {
	var present uint64
	for u := 1; u <= 10; u++ {
		present |= 1 << uint(u-1)
	}
	return present | 1<<19 // U20
}

func newTestDevice(t *testing.T, mutate func(*Snapshot)) *Device {
	t.Helper()
	const uCount = 42
	present := testPresent()
	seed, err := ParseTagHex("E200000000000000")
	if err != nil {
		t.Fatal(err)
	}
	tagBytes := GenerateTags(uCount, present, seed, 6, 2, nil)
	tags := make([]string, uCount)
	for i := range tagBytes {
		tags[i] = FormatTagHex(tagBytes[i])
	}
	snap := Snapshot{
		ID:            "test-dev",
		Name:          "test",
		UCount:        uCount,
		Model:         0x8020,
		BaudCode:      3,
		SoftVersion:   0x0100,
		HardVersion:   0x0100,
		ESN:           "TEST-ESN",
		AdoptAddress:  true,
		ChangedHoldMS: 5000,
		TCPUnitID:     1,
		Tags:          tags,
		Present:       present,
		Bus:           [3]uint16{1, 1, 0},
		LED:           make([]uint16, uCount),
		Climate:       climate.Params{Enabled: false, Timezone: "UTC", TimeScale: 1, UpdateMS: 5000},
		FixedTH:       [2]float64{26.5, 48.0},
	}
	if mutate != nil {
		mutate(&snap)
	}
	dev, err := New(snap, time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dev.SetPrimaryAddress(1)
	return dev
}

func TestInputRegisters(t *testing.T) {
	dev := newTestDevice(t, nil)

	words, code := dev.ReadInput(0, 4, 1)
	if code != 0 || !reflect.DeepEqual(words, []uint16{0xE200, 0, 0, 0x0001}) {
		t.Fatalf("U1 tag = %#v code=%d", words, code)
	}
	// U20 starts at (20-1)*4 = 76 = 0x4C.
	words, code = dev.ReadInput(0x4C, 4, 1)
	if code != 0 || !reflect.DeepEqual(words, []uint16{0xE200, 0, 0, 0x0014}) {
		t.Fatalf("U20 tag = %#v code=%d", words, code)
	}
	// U42 is absent and therefore zero.
	words, code = dev.ReadInput(0xA4, 4, 1)
	if code != 0 || !reflect.DeepEqual(words, []uint16{0, 0, 0, 0}) {
		t.Fatalf("U42 tag = %#v code=%d", words, code)
	}

	words, code = dev.ReadInput(0x0100, 6, 1)
	want := []uint16{0x8560, 0x8560, 0x8560, 1, 1, 0}
	if code != 0 || !reflect.DeepEqual(words, want) {
		t.Fatalf("TH/bus = %#v code=%d", words, code)
	}

	words, code = dev.ReadInput(0x0150, 10, 1)
	if code != 0 || words[0] != 0x5445 { // 'T','E'
		t.Fatalf("ESN words = %#v code=%d", words, code)
	}

	words, code = dev.ReadInput(0x0200, 1, 1)
	if code != 0 || len(words) != 1 || words[0] != 0 {
		t.Fatalf("lenient unknown read = %#v code=%d", words, code)
	}
	if _, code = dev.ReadInput(0x6000, 1, 1); code != ExcIllegalAddress {
		t.Fatalf("out of range code=%d", code)
	}
	if _, code = dev.ReadInput(0, 0, 1); code != ExcIllegalAddress {
		t.Fatalf("zero count code=%d", code)
	}
}

func TestStrictMode(t *testing.T) {
	dev := newTestDevice(t, func(s *Snapshot) { s.Strict = true })
	if _, code := dev.ReadInput(0x0200, 1, 1); code != ExcIllegalAddress {
		t.Fatalf("strict input unknown code=%d", code)
	}
	if _, code := dev.ReadHolding(0x0200, 1, 1); code != ExcIllegalAddress {
		t.Fatalf("strict holding unknown code=%d", code)
	}
	if _, code := dev.ReadInput(0x0100, 3, 1); code != 0 {
		t.Fatalf("strict defined read code=%d", code)
	}
	if code := dev.WriteHolding(0x0200, 1, 1); code != ExcIllegalAddress {
		t.Fatalf("strict unknown write code=%d", code)
	}
}

func TestHoldingRegisters(t *testing.T) {
	dev := newTestDevice(t, nil)

	words, code := dev.ReadHolding(0, 6, 1)
	want := []uint16{0x8020, 1, 3, 42, 0x0100, 0x0100}
	if code != 0 || !reflect.DeepEqual(words, want) {
		t.Fatalf("info = %#v code=%d", words, code)
	}
	if words, _ = dev.ReadHolding(1, 1, 5); words[0] != 5 {
		t.Fatalf("dynamic address = %d", words[0])
	}
	if code = dev.WriteHolding(0x0100, 0x00A0, 1); code != 0 {
		t.Fatalf("write LED code=%d", code)
	}
	if words, _ = dev.ReadHolding(0x0100, 1, 1); words[0] != 0x00A0 {
		t.Fatalf("LED read = %#x", words[0])
	}
	if got := dev.Snapshot().LED[0]; got != 0x00A0 {
		t.Fatalf("snapshot LED = %#x", got)
	}
	if code = dev.WriteHolding(0x0010, 0x1234, 1); code != 0 {
		t.Fatalf("lenient unknown write code=%d", code)
	}
	if words, _ = dev.ReadHolding(0x0010, 1, 1); words[0] != 0x1234 {
		t.Fatalf("lenient unknown readback = %#x", words[0])
	}
	if code = dev.WriteHoldingMany(0x0101, []uint16{1, 2, 3}, 1); code != 0 {
		t.Fatalf("multi write code=%d", code)
	}
	if words, _ = dev.ReadHolding(0x0101, 3, 1); !reflect.DeepEqual(words, []uint16{1, 2, 3}) {
		t.Fatalf("multi readback = %#v", words)
	}
}

func TestDiscreteBitsAndChanged(t *testing.T) {
	dev := newTestDevice(t, nil)
	bits, code := dev.ReadDiscrete(0, 42)
	if code != 0 {
		t.Fatalf("discrete code=%d", code)
	}
	var present uint64
	for i, v := range bits {
		if v {
			present |= 1 << uint(i)
		}
	}
	if present != testPresent() {
		t.Fatalf("present bits = %#x", present)
	}
	if err := dev.PulseChanged(3); err != nil {
		t.Fatal(err)
	}
	bits, _ = dev.ReadDiscrete(0x0100, 42)
	if !bits[2] {
		t.Fatal("changed bit U3 should be asserted")
	}
	if dev.ChangedBits()&(1<<2) == 0 {
		t.Fatal("ChangedBits missing U3")
	}
	dev.ClearChanged()
	if dev.ChangedBits() != 0 {
		t.Fatal("changed bits should be cleared")
	}

	fast := newTestDevice(t, func(s *Snapshot) { s.ChangedHoldMS = 1 })
	if err := fast.PulseChanged(1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if fast.ChangedBits() != 0 {
		t.Fatal("changed bit should expire")
	}
}

func TestPresentAndTags(t *testing.T) {
	dev := newTestDevice(t, nil)
	if err := dev.SetPresent(11, true); err != nil {
		t.Fatal(err)
	}
	words, _ := dev.ReadInput(0x28, 4, 1) // U11
	if words[3] != 0x000B {
		t.Fatalf("generated tag = %#v", words)
	}
	if err := dev.SetPresent(11, false); err != nil {
		t.Fatal(err)
	}
	words, _ = dev.ReadInput(0x28, 4, 1)
	if words[0] != 0 || words[1] != 0 || words[2] != 0 || words[3] != 0 {
		t.Fatalf("cleared tag = %#v", words)
	}
	custom := [8]byte{1, 2, 3, 4, 5, 6, 7, 8}
	if err := dev.SetTag(2, custom); err != nil {
		t.Fatal(err)
	}
	words, _ = dev.ReadInput(4, 4, 1) // U2
	if !reflect.DeepEqual(words, []uint16{0x0102, 0x0304, 0x0506, 0x0708}) {
		t.Fatalf("custom tag = %#v", words)
	}
}

func TestShadowMap(t *testing.T) {
	dev := newTestDevice(t, nil)

	words, code := dev.ReadShadowHolding(0x8000, 6)
	if code != 0 || !reflect.DeepEqual(words, []uint16{0x8020, 1, 3, 42, 0x0100, 0x0100}) {
		t.Fatalf("shadow info = %#v code=%d", words, code)
	}
	if code = dev.WriteShadowHolding(0x8010, 0xAAAA); code != 0 {
		t.Fatalf("tag write code=%d", code)
	}
	if words, _ = dev.ReadShadowHolding(0x8010, 1); words[0] != 0xAAAA {
		t.Fatalf("tag readback = %#x", words[0])
	}
	if code = dev.WriteShadowHolding(0x80C0, 0x8A70); code != 0 {
		t.Fatalf("TH write code=%d", code)
	}
	if words, _ = dev.ReadInput(0x0100, 1, 1); words[0] != 0x8A70 {
		t.Fatalf("TH readback = %#x", words[0])
	}
	if dev.ManualMask()&1 == 0 {
		t.Fatal("TH write should freeze the top zone")
	}
	if code = dev.WriteShadowHolding(0x80C3, 0); code != 0 {
		t.Fatalf("bus write code=%d", code)
	}
	if words, _ = dev.ReadInput(0x0103, 1, 1); words[0] != 0 {
		t.Fatalf("bus readback = %#x", words[0])
	}
	if code = dev.WriteShadowHolding(0x80D0, 0x00A0); code != 0 {
		t.Fatalf("LED shadow write code=%d", code)
	}
	if words, _ = dev.ReadHolding(0x0100, 1, 1); words[0] != 0x00A0 {
		t.Fatalf("LED native readback = %#x", words[0])
	}
	if code = dev.WriteShadowHolding(0x8100, 0x000F); code != 0 {
		t.Fatalf("presence write code=%d", code)
	}
	bits, _ := dev.ReadDiscrete(0, 4)
	if !bits[0] || !bits[1] || !bits[2] || !bits[3] {
		t.Fatalf("presence bits = %#v", bits)
	}
	if code = dev.WriteShadowHolding(0x8110, 0x0001); code != 0 {
		t.Fatalf("changed write code=%d", code)
	}
	if dev.ChangedBits()&1 == 0 {
		t.Fatal("shadow changed write did not assert U1")
	}
	if code = dev.WriteShadowHolding(0x8120, 1); code != 0 {
		t.Fatalf("command write code=%d", code)
	}
	if dev.ChangedBits() != 0 {
		t.Fatal("command 1 should clear changed bits")
	}
	if code = dev.WriteShadowHolding(0x8000, 1); code != ExcIllegalAddress {
		t.Fatalf("info write code=%d", code)
	}
	if code = dev.WriteShadowHolding(0x8121, 1); code != ExcIllegalAddress {
		t.Fatalf("mask write code=%d", code)
	}
	if words, _ = dev.ReadShadowHolding(0x8121, 1); words[0] != 1 {
		t.Fatalf("manual mask register = %#x", words[0])
	}

	// Coils: release the top zone and clear the manual through the command.
	if code = dev.WriteShadowCoil(0x8060, false); code != 0 {
		t.Fatalf("manual coil release code=%d", code)
	}
	if dev.ManualMask() != 0 {
		t.Fatal("manual mask should be zero")
	}
	if code = dev.WriteShadowCoil(0x8060, true); code != 0 {
		t.Fatalf("manual coil freeze code=%d", code)
	}
	if dev.ManualMask()&1 == 0 {
		t.Fatal("manual coil should freeze top")
	}
	if code = dev.WriteShadowHolding(0x8120, 2); code != 0 {
		t.Fatalf("command 2 code=%d", code)
	}
	if dev.ManualMask() != 0 {
		t.Fatal("command 2 should clear all manual overrides")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dev := newTestDevice(t, nil)
	dev.SetName("renamed")
	if err := dev.SetESN("NEW-ESN"); err != nil {
		t.Fatal(err)
	}
	if err := dev.SetLED(2, 0x1234); err != nil {
		t.Fatal(err)
	}
	if err := dev.SetTag(1, [8]byte{9, 8, 7, 6, 5, 4, 3, 2}); err != nil {
		t.Fatal(err)
	}
	if err := dev.SetPresent(30, true); err != nil {
		t.Fatal(err)
	}
	dev.SetBus(false, true, true)
	dev.SetTCPUnit(9)
	snap := dev.Snapshot()

	clone, err := New(snap, time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := clone.Snapshot()
	if got.Name != "renamed" || got.ESN != "NEW-ESN" || got.TCPUnitID != 9 {
		t.Fatalf("metadata mismatch: %+v", got)
	}
	if got.LED[1] != 0x1234 {
		t.Fatalf("LED mismatch: %#v", got.LED)
	}
	if got.Tags[0] != "0908070605040302" {
		t.Fatalf("tag mismatch: %s", got.Tags[0])
	}
	if got.Present&(1<<29) == 0 {
		t.Fatal("present bit U30 missing")
	}
	if got.Bus != [3]uint16{0, 1, 1} {
		t.Fatalf("bus mismatch: %#v", got.Bus)
	}
}

func TestSetUCount(t *testing.T) {
	dev := newTestDevice(t, nil)
	if err := dev.SetUCount(20); err != nil {
		t.Fatal(err)
	}
	if dev.UCount() != 20 {
		t.Fatalf("u_count = %d", dev.UCount())
	}
	if _, code := dev.ReadInput(0x50, 1, 1); code != 0 {
		// U21 is beyond the new U count but still a legal lenient read.
		t.Fatalf("lenient read after resize code=%d", code)
	}
	words, _ := dev.ReadHolding(3, 1, 1)
	if words[0] != 20 {
		t.Fatalf("info U count = %d", words[0])
	}
	if err := dev.SetPresent(30, true); err == nil {
		t.Fatal("U30 should be out of range after resize")
	}
}

func TestClimateFixedAndManual(t *testing.T) {
	dev := newTestDevice(t, nil)
	words, _ := dev.ReadInput(0x0100, 3, 1)
	if !reflect.DeepEqual(words, []uint16{0x8560, 0x8560, 0x8560}) {
		t.Fatalf("fixed TH = %#v", words)
	}
	dev.SetManual(climate.Top, climate.QuantTemp(30), climate.QuantHumi(60), time.Time{})
	words, _ = dev.ReadInput(0x0100, 1, 1)
	want := uint16(climate.QuantTemp(30))<<8 | uint16(climate.QuantHumi(60))
	if words[0] != want {
		t.Fatalf("manual TH = %#x want %#x", words[0], want)
	}
	if dev.ManualMask()&1 == 0 {
		t.Fatal("manual mask missing top")
	}
	dev.ClearManual(climate.Top)
	words, _ = dev.ReadInput(0x0100, 1, 1)
	if words[0] != 0x8560 {
		t.Fatalf("cleared TH = %#x", words[0])
	}
}

func TestClimateEnabledZones(t *testing.T) {
	params := climate.Params{
		Enabled:   true,
		Timezone:  "UTC",
		TimeScale: 1,
		UpdateMS:  5000,
		Temperature: climate.Temperature{
			Mean: 25, Amplitude: 2, PeakHour: 15, Min: 18, Max: 30,
		},
		Humidity: climate.Humidity{
			Mean: 50, TempCoupling: 1.2, Amplitude: 1.5, Min: 30, Max: 70,
		},
		ZoneOffsets: climate.ZoneOffsets{
			Top:    climate.ZoneOffset{Temp: 0.8, Humi: -1.0},
			Mid:    climate.ZoneOffset{},
			Bottom: climate.ZoneOffset{Temp: -0.6, Humi: 1.0},
		},
		Noise: climate.Noise{TempTauMS: 180000, HumiTauMS: 180000},
	}
	dev := newTestDevice(t, func(s *Snapshot) {
		s.Climate = params
	})
	words, _ := dev.ReadInput(0x0100, 3, 1)
	temps := []float64{
		climate.TempFromRaw(uint8(words[0] >> 8)),
		climate.TempFromRaw(uint8(words[1] >> 8)),
		climate.TempFromRaw(uint8(words[2] >> 8)),
	}
	if !(temps[0] > temps[1] && temps[1] > temps[2]) {
		t.Fatalf("expected top > mid > bottom, got %v", temps)
	}
	humi := []float64{
		climate.HumiFromRaw(uint8(words[0])),
		climate.HumiFromRaw(uint8(words[1])),
		climate.HumiFromRaw(uint8(words[2])),
	}
	if !(humi[0] < humi[1] && humi[1] < humi[2]) {
		t.Fatalf("expected top < mid < bottom humidity, got %v", humi)
	}
}

func TestTickAdvancesSimulatedTime(t *testing.T) {
	// newTestDevice seeds the climate engine at 12:00 UTC.
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	params := climate.Params{
		Enabled:   true,
		Timezone:  "UTC",
		TimeScale: 1440,
		UpdateMS:  100,
		Temperature: climate.Temperature{
			Mean: 25, Amplitude: 2, PeakHour: 15, Min: 18, Max: 30,
		},
		Humidity: climate.Humidity{
			Mean: 50, TempCoupling: 1.2, Amplitude: 1.5, Min: 30, Max: 70,
		},
		ZoneOffsets: climate.ZoneOffsets{},
		Noise:       climate.Noise{TempTauMS: 180000, HumiTauMS: 180000},
	}
	dev := newTestDevice(t, func(s *Snapshot) {
		s.Climate = params
	})
	before, _ := dev.ReadInput(0x0101, 1, 1)
	dev.Tick(now.Add(30 * time.Second)) // 30s * 1440 = 12h -> midnight
	after, _ := dev.ReadInput(0x0101, 1, 1)
	if before[0] == after[0] {
		t.Fatalf("expected simulated time to change TH: %#x", before[0])
	}
}
