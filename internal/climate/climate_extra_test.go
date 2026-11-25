package climate

import (
	"math"
	"math/rand"
	"testing"
	"time"
)

func TestNewAndSetParamsDefaults(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	p := baseParams()
	p.TimeScale = 0
	p.UpdateMS = 0
	e := New(p, 3, now)
	got := e.Params()
	if got.TimeScale != 1.0 || got.UpdateMS != 5000 {
		t.Fatalf("defaults = scale %v update %d", got.TimeScale, got.UpdateMS)
	}
	if e.Current() == ([Zones]uint16{}) {
		t.Fatal("engine did not compute initial values")
	}

	p.Timezone = "Nowhere/Invalid"
	e2 := New(p, 3, now)
	if e2.Params().Timezone != "Nowhere/Invalid" {
		t.Fatal("Params should preserve the configured timezone string")
	}
	if e2.Current() == ([Zones]uint16{}) {
		t.Fatal("invalid timezone should fall back to UTC, not zero values")
	}

	e.SetManual(Top, 140, 120, time.Time{})
	e.SetParams(baseParams())
	if e.ManualMask()&1 == 0 {
		t.Fatal("SetParams should preserve manual overrides")
	}
	p = baseParams()
	p.TimeScale = 0
	p.UpdateMS = -5
	p.Timezone = "Bad/Zone"
	e.SetParams(p)
	got = e.Params()
	if got.TimeScale != 1.0 || got.UpdateMS != 5000 {
		t.Fatalf("SetParams defaults = scale %v update %d", got.TimeScale, got.UpdateMS)
	}
}

func TestManualStatesAndClearAll(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, now)
	until := now.Add(time.Hour)
	e.SetManual(Top, 140, 120, until)
	state := e.ManualStates()[Top]
	if !state.Enabled || state.TempRaw != 140 || state.HumiRaw != 120 || !state.Until.Equal(until) {
		t.Fatalf("manual state = %+v", state)
	}
	var states [Zones]ManualState
	states[Mid] = ManualState{Enabled: true, TempRaw: 130, HumiRaw: 100}
	e.SetManualStates(states)
	if e.ManualMask() != 0b010 {
		t.Fatalf("mask after SetManualStates = %#b", e.ManualMask())
	}
	if e.Current()[Mid] != rawWord(130, 100) {
		t.Fatalf("manual mid current = %#x", e.Current()[Mid])
	}
	e.ClearAllManual()
	if e.ManualMask() != 0 {
		t.Fatal("ClearAllManual left overrides")
	}
}

func TestZoneClamping(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, now)
	e.SetManual(Zone(-3), 140, 120, time.Time{})
	if e.ManualMask() != 0b001 {
		t.Fatalf("negative zone mask = %#b", e.ManualMask())
	}
	e.ClearManual(Zone(-3))
	e.SetManual(Zone(99), 130, 110, time.Time{})
	if e.ManualMask() != 0b100 {
		t.Fatalf("oversized zone mask = %#b", e.ManualMask())
	}
	e.ClearManual(Zone(99))
	if e.ManualMask() != 0 {
		t.Fatal("clamped clear did not clear the bottom zone")
	}
	e.SetManual(Zone(1), 1, 2, time.Time{})
	e.ClearManual(Zone(1))
	if e.ManualMask() != 0 {
		t.Fatal("clear of middle zone failed")
	}
}

func TestExpireManualWithoutDeadline(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, now)
	e.SetManual(Top, 140, 120, time.Time{})
	if e.ExpireManual(now.Add(24 * time.Hour)) {
		t.Fatal("zero deadline must never expire")
	}
	if e.ManualMask() != 1 {
		t.Fatal("manual override unexpectedly cleared")
	}
	e.SetManual(Mid, 130, 110, now.Add(time.Second))
	if !e.ExpireManual(now.Add(2 * time.Second)) {
		t.Fatal("expired override not reported")
	}
	if e.ManualMask() != 1 {
		t.Fatalf("mask = %#b", e.ManualMask())
	}
	if e.ExpireManual(now.Add(3 * time.Second)) {
		t.Fatal("second expiry should report no change")
	}
}

func TestValidateParams(t *testing.T) {
	valid := baseParams()
	if err := ValidateParams(valid); err != nil {
		t.Fatalf("base params invalid: %v", err)
	}
	nan := math.NaN()
	inf := math.Inf(1)
	cases := []struct {
		name   string
		mutate func(*Params)
	}{
		{"update too small", func(p *Params) { p.UpdateMS = 99 }},
		{"time scale zero", func(p *Params) { p.TimeScale = 0 }},
		{"time scale huge", func(p *Params) { p.TimeScale = 100001 }},
		{"bad timezone", func(p *Params) { p.Timezone = "Mars/Olympus" }},
		{"temperature NaN", func(p *Params) { p.Temperature.Mean = nan }},
		{"temperature inf", func(p *Params) { p.Temperature.Amplitude = inf }},
		{"temperature peak NaN", func(p *Params) { p.Temperature.PeakHour = nan }},
		{"temperature harmonic NaN", func(p *Params) { p.Temperature.Harmonic2Amplitude = nan }},
		{"temperature min NaN", func(p *Params) { p.Temperature.Min = nan }},
		{"temperature max NaN", func(p *Params) { p.Temperature.Max = nan }},
		{"temperature reversed", func(p *Params) { p.Temperature.Min = 30; p.Temperature.Max = 30 }},
		{"temperature low", func(p *Params) { p.Temperature.Min = -41 }},
		{"temperature high", func(p *Params) { p.Temperature.Max = 88 }},
		{"temperature negative amplitude", func(p *Params) { p.Temperature.Amplitude = -1 }},
		{"harmonic negative", func(p *Params) { p.Temperature.Harmonic2Amplitude = -1 }},
		{"humidity NaN", func(p *Params) { p.Humidity.Mean = nan }},
		{"humidity coupling NaN", func(p *Params) { p.Humidity.TempCoupling = nan }},
		{"humidity amplitude NaN", func(p *Params) { p.Humidity.Amplitude = nan }},
		{"humidity min NaN", func(p *Params) { p.Humidity.Min = nan }},
		{"humidity max NaN", func(p *Params) { p.Humidity.Max = nan }},
		{"humidity reversed", func(p *Params) { p.Humidity.Min = 60; p.Humidity.Max = 50 }},
		{"humidity low", func(p *Params) { p.Humidity.Min = -1 }},
		{"humidity high", func(p *Params) { p.Humidity.Max = 101 }},
		{"humidity negative amplitude", func(p *Params) { p.Humidity.Amplitude = -1 }},
		{"zone temp NaN", func(p *Params) { p.ZoneOffsets.Top.Temp = nan }},
		{"zone humi NaN", func(p *Params) { p.ZoneOffsets.Mid.Humi = nan }},
		{"zone lag NaN", func(p *Params) { p.ZoneOffsets.Bottom.LagH = nan }},
		{"noise tau zero", func(p *Params) { p.Noise.TempTauMS = 0 }},
		{"noise humi tau zero", func(p *Params) { p.Noise.HumiTauMS = 0 }},
		{"noise sigma NaN", func(p *Params) { p.Noise.TempSigma = nan }},
		{"noise sigma negative", func(p *Params) { p.Noise.HumiSigma = -1 }},
	}
	for _, tc := range cases {
		p := baseParams()
		tc.mutate(&p)
		if err := ValidateParams(p); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
	}
}

func TestFiniteAndClampHelpers(t *testing.T) {
	if !finite(1) || !finite(0) || finite(math.NaN()) || finite(math.Inf(-1)) {
		t.Fatal("finite helper mismatch")
	}
	if clampFloat(-1, 0, 10) != 0 || clampFloat(11, 0, 10) != 10 || clampFloat(5, 0, 10) != 5 {
		t.Fatal("clampFloat mismatch")
	}
	if clampU8(-1) != 0 || clampU8(256) != 255 || clampU8(7) != 7 {
		t.Fatal("clampU8 mismatch")
	}
	if clampZone(Zone(-1)) != Top || clampZone(Zone(3)) != Bottom || clampZone(Mid) != Mid {
		t.Fatal("clampZone mismatch")
	}
	if ouStep(1.5, 1, 0, 1, nil) != 1.5 || ouStep(1.5, 1, 1, 0, nil) != 1.5 {
		t.Fatal("ouStep should be a no-op for invalid tau/sigma")
	}
	if got := ouStep(0, 1, 10, 1, rand.New(rand.NewSource(1))); math.IsNaN(got) || math.IsInf(got, 0) {
		t.Fatalf("ouStep produced %v", got)
	}
}

func TestRefreshAndTickEdges(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, now)
	cur := e.Refresh(now.Add(-time.Minute))
	if cur != e.Current() {
		t.Fatal("Refresh should update Current")
	}
	e.Refresh(now)
	_, changed := e.Tick(now.Add(10 * time.Millisecond))
	if changed {
		t.Fatal("tick immediately after refresh should not change values")
	}
	e.Refresh(now.Add(2 * time.Hour))
	if e.Current() == ([Zones]uint16{}) {
		t.Fatal("long refresh produced zero values")
	}
	var p Params
	e2 := New(p, 1, now)
	if e2.Current() == ([Zones]uint16{}) {
		t.Fatal("zero params should still compute values")
	}
	e2.SetManual(Top, 0, 0, time.Time{})
	if e2.Current()[Top] != 0 {
		t.Fatalf("manual zero word = %#x", e2.Current()[Top])
	}
}

func TestManualAllZonesIgnoresCurve(t *testing.T) {
	now := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, now)
	for z := Zone(0); z < Zones; z++ {
		e.SetManual(z, uint8(100+z), uint8(50+z), time.Time{})
	}
	var states [Zones]ManualState
	for z := Zone(0); z < Zones; z++ {
		states[z] = ManualState{Enabled: true, TempRaw: uint8(100 + z), HumiRaw: uint8(50 + z)}
	}
	e.SetManualStates(states)
	for z := Zone(0); z < Zones; z++ {
		if got, want := e.Current()[z], rawWord(uint8(100+z), uint8(50+z)); got != want {
			t.Fatalf("zone %d current = %#x want %#x", z, got, want)
		}
	}
}
