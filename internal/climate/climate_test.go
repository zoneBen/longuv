package climate

import (
	"math"
	"testing"
	"time"
)

func baseParams() Params {
	return Params{
		Enabled:   true,
		Timezone:  "UTC",
		TimeScale: 1,
		UpdateMS:  5000,
		Temperature: Temperature{
			Mean: 25, Amplitude: 2, PeakHour: 15, Harmonic2Amplitude: 0,
			Min: 18, Max: 30,
		},
		Humidity: Humidity{
			Mean: 50, TempCoupling: 1.2, Amplitude: 1.5, Min: 30, Max: 70,
		},
		ZoneOffsets: ZoneOffsets{
			Top:    ZoneOffset{Temp: 0.8, Humi: -1.0},
			Mid:    ZoneOffset{},
			Bottom: ZoneOffset{Temp: -0.6, Humi: 1.0},
		},
		Noise: Noise{TempTauMS: 180000, HumiTauMS: 180000},
	}
}

func TestQuantization(t *testing.T) {
	cases := []struct {
		temp float64
		raw  uint8
	}{
		{26.5, 0x85},
		{26.25, 132}, // banker's rounding: 132.5 -> 132
		{-40, 0},
		{87.5, 255},
		{100, 255},
		{-100, 0},
	}
	for _, tc := range cases {
		if got := QuantTemp(tc.temp); got != tc.raw {
			t.Fatalf("QuantTemp(%v) = %d want %d", tc.temp, got, tc.raw)
		}
	}
	humi := []struct {
		pct float64
		raw uint8
	}{
		{48, 96},
		{48.25, 96}, // 96.5 -> 96
		{0, 0},
		{100, 200},
		{120, 200},
		{-5, 0},
	}
	for _, tc := range humi {
		if got := QuantHumi(tc.pct); got != tc.raw {
			t.Fatalf("QuantHumi(%v) = %d want %d", tc.pct, got, tc.raw)
		}
	}
	if math.Abs(TempFromRaw(0x85)-26.5) > 1e-9 {
		t.Fatal("TempFromRaw mismatch")
	}
	if math.Abs(HumiFromRaw(0x60)-48.0) > 1e-9 {
		t.Fatal("HumiFromRaw mismatch")
	}
}

func TestDailyCurveZoneOrdering(t *testing.T) {
	atPeak := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, atPeak)
	cur := e.Current()
	temps := []float64{
		TempFromRaw(uint8(cur[Top] >> 8)),
		TempFromRaw(uint8(cur[Mid] >> 8)),
		TempFromRaw(uint8(cur[Bottom] >> 8)),
	}
	if !(temps[0] > temps[1] && temps[1] > temps[2]) {
		t.Fatalf("expected top > mid > bottom, got %v", temps)
	}
	humi := []float64{
		HumiFromRaw(uint8(cur[Top])),
		HumiFromRaw(uint8(cur[Mid])),
		HumiFromRaw(uint8(cur[Bottom])),
	}
	if !(humi[0] < humi[1] && humi[1] < humi[2]) {
		t.Fatalf("expected top < mid < bottom humidity, got %v", humi)
	}

	atNight := time.Date(2026, 11, 25, 3, 0, 0, 0, time.UTC)
	night := New(baseParams(), 1, atNight)
	dayTemp := TempFromRaw(uint8(e.Current()[Mid] >> 8))
	nightTemp := TempFromRaw(uint8(night.Current()[Mid] >> 8))
	if !(dayTemp > nightTemp+1.0) {
		t.Fatalf("expected a clear day/night difference: day=%v night=%v", dayTemp, nightTemp)
	}
	dayHumi := HumiFromRaw(uint8(e.Current()[Mid]))
	nightHumi := HumiFromRaw(uint8(night.Current()[Mid]))
	if !(nightHumi > dayHumi+1.0) {
		t.Fatalf("expected humidity to rise at night: day=%v night=%v", dayHumi, nightHumi)
	}
}

func TestManualOverrideAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, now)
	e.SetManual(Top, 140, 120, time.Time{})
	if e.Current()[Top] != 0x8C78 {
		t.Fatalf("manual top = %#x", e.Current()[Top])
	}
	if e.ManualMask() != 1 {
		t.Fatalf("manual mask = %d", e.ManualMask())
	}
	e.SetManual(Mid, 130, 110, now.Add(time.Second))
	if e.ManualMask() != 0b11 {
		t.Fatalf("manual mask = %d", e.ManualMask())
	}
	cur, changed := e.Tick(now.Add(2 * time.Second))
	if !changed {
		t.Fatal("expiry should report a change")
	}
	if e.ManualMask() != 1 {
		t.Fatalf("mid should have expired, mask=%d", e.ManualMask())
	}
	if cur[Mid] == 0x826E {
		t.Fatal("mid should have returned to automatic control")
	}
	e.ClearManual(Top)
	if e.ManualMask() != 0 {
		t.Fatal("top should be released")
	}
}

func TestUpdateInterval(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	e := New(baseParams(), 1, now)
	before := e.Current()
	cur, changed := e.Tick(now.Add(time.Second))
	if changed || cur != before {
		t.Fatal("tick before the update interval should not change values")
	}
}

func TestTimeScaleAdvancesSimulation(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	p := baseParams()
	p.TimeScale = 12
	p.UpdateMS = 100
	e := New(p, 1, now)
	before := e.Current()[Mid]
	// One real hour at 12x is twelve simulated hours: noon -> midnight.
	after, changed := e.Tick(now.Add(time.Hour))
	if !changed {
		t.Fatal("accelerated simulation should change values")
	}
	if after[Mid] == before {
		t.Fatalf("expected noon/midnight difference, both %#x", before)
	}
}

func TestNoiseStaysWithinConfiguredBounds(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	p := baseParams()
	p.UpdateMS = 100
	p.Temperature.Amplitude = 0
	p.Temperature.Min = 24
	p.Temperature.Max = 26
	p.Humidity.Amplitude = 0
	p.Humidity.TempCoupling = 0
	p.Humidity.Min = 44
	p.Humidity.Max = 56
	p.Noise = Noise{TempTauMS: 1000, TempSigma: 1.0, HumiTauMS: 1000, HumiSigma: 3.0}
	e := New(p, 7, now)
	for i := 1; i <= 200; i++ {
		e.Tick(now.Add(time.Duration(i) * 200 * time.Millisecond))
		for z := 0; z < int(Zones); z++ {
			tv := TempFromRaw(uint8(e.Current()[z] >> 8))
			hv := HumiFromRaw(uint8(e.Current()[z]))
			if tv < 23.5 || tv > 26.5 {
				t.Fatalf("temperature %v outside bounds", tv)
			}
			if hv < 43.5 || hv > 56.5 {
				t.Fatalf("humidity %v outside bounds", hv)
			}
		}
	}
}

func TestManualSurvivesTick(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	p := baseParams()
	p.UpdateMS = 100
	e := New(p, 1, now)
	e.SetManual(Top, 0x8C, 0x78, time.Time{})
	for i := 1; i <= 10; i++ {
		e.Tick(now.Add(time.Duration(i) * time.Second))
	}
	if e.Current()[Top] != 0x8C78 {
		t.Fatalf("manual value was overwritten: %#x", e.Current()[Top])
	}
}
