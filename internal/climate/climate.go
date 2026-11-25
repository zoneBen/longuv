// Package climate implements the realistic daily temperature/humidity model
// used by simulated asset strips: a daily sinusoid, per-zone offsets and lag,
// Ornstein-Uhlenbeck noise, and per-zone manual overrides.
package climate

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

// Params is the persisted climate configuration of one device.
type Params struct {
	Enabled     bool        `json:"enabled" yaml:"enabled"`
	Timezone    string      `json:"timezone" yaml:"timezone"`
	TimeScale   float64     `json:"time_scale" yaml:"time_scale"`
	UpdateMS    int         `json:"update_ms" yaml:"update_ms"`
	Temperature Temperature `json:"temperature" yaml:"temperature"`
	Humidity    Humidity    `json:"humidity" yaml:"humidity"`
	ZoneOffsets ZoneOffsets `json:"zone_offsets" yaml:"zone_offsets"`
	Noise       Noise       `json:"noise" yaml:"noise"`
}

// Temperature is the ambient daily curve.
type Temperature struct {
	Mean               float64 `json:"mean" yaml:"mean"`
	Amplitude          float64 `json:"amplitude" yaml:"amplitude"`
	PeakHour           float64 `json:"peak_hour" yaml:"peak_hour"`
	Harmonic2Amplitude float64 `json:"harmonic2_amplitude" yaml:"harmonic2_amplitude"`
	Min                float64 `json:"min" yaml:"min"`
	Max                float64 `json:"max" yaml:"max"`
}

// Humidity is the humidity curve, inversely coupled to temperature.
type Humidity struct {
	Mean         float64 `json:"mean" yaml:"mean"`
	TempCoupling float64 `json:"temp_coupling" yaml:"temp_coupling"`
	Amplitude    float64 `json:"amplitude" yaml:"amplitude"`
	Min          float64 `json:"min" yaml:"min"`
	Max          float64 `json:"max" yaml:"max"`
}

// ZoneOffsets describes top/middle/bottom sensor deviations and lag.
type ZoneOffsets struct {
	Top    ZoneOffset `json:"top" yaml:"top"`
	Mid    ZoneOffset `json:"mid" yaml:"mid"`
	Bottom ZoneOffset `json:"bottom" yaml:"bottom"`
}

// ZoneOffset is the deviation and daily lag for one sensor.
type ZoneOffset struct {
	Temp float64 `json:"temp" yaml:"temp"`
	Humi float64 `json:"humi" yaml:"humi"`
	LagH float64 `json:"lag_h" yaml:"lag_h"`
}

// Noise configures the Ornstein-Uhlenbeck noise process.
type Noise struct {
	TempTauMS int     `json:"temp_tau_ms" yaml:"temp_tau_ms"`
	TempSigma float64 `json:"temp_sigma" yaml:"temp_sigma"`
	HumiTauMS int     `json:"humi_tau_ms" yaml:"humi_tau_ms"`
	HumiSigma float64 `json:"humi_sigma" yaml:"humi_sigma"`
}

// ManualState freezes one zone at explicit raw register bytes.
type ManualState struct {
	Enabled bool      `json:"enabled"`
	TempRaw uint8     `json:"temp_raw"`
	HumiRaw uint8     `json:"humi_raw"`
	Until   time.Time `json:"until"`
}

// Zone identifies a sensor zone (0=top, 1=middle, 2=bottom).
type Zone int

const (
	Top Zone = iota
	Mid
	Bottom
)

// Zones is the fixed zone count of an asset strip.
const Zones = 3

// Engine computes the current raw temperature/humidity words for one device.
// It is not safe for concurrent use; the owning Device serialises access.
type Engine struct {
	params Params
	loc    *time.Location
	rng    *rand.Rand

	start      time.Time
	lastReal   time.Time
	lastUpdate time.Time

	manual [Zones]ManualState
	cur    [Zones]uint16

	commonTemp float64
	commonHumi float64
	zoneTemp   [Zones]float64
	zoneHumi   [Zones]float64
}

// New builds an engine seeded deterministically per device.
func New(params Params, seed int64, now time.Time) *Engine {
	if params.TimeScale <= 0 {
		params.TimeScale = 1.0
	}
	if params.UpdateMS <= 0 {
		params.UpdateMS = 5000
	}
	loc, err := time.LoadLocation(params.Timezone)
	if err != nil {
		loc = time.UTC
	}
	e := &Engine{
		params:     params,
		loc:        loc,
		rng:        rand.New(rand.NewSource(seed)),
		start:      now,
		lastReal:   now,
		lastUpdate: now,
	}
	e.cur = e.compute(now)
	return e
}

// Params returns the current parameters (a copy).
func (e *Engine) Params() Params { return e.params }

// SetParams replaces the parameters, preserving manual overrides.
func (e *Engine) SetParams(p Params) {
	if p.TimeScale <= 0 {
		p.TimeScale = 1.0
	}
	if p.UpdateMS <= 0 {
		p.UpdateMS = 5000
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		loc = time.UTC
	}
	e.params = p
	e.loc = loc
	e.start = time.Now()
	e.lastReal = e.start
	e.lastUpdate = e.start
}

// ManualStates returns a copy of the manual override states.
func (e *Engine) ManualStates() [Zones]ManualState { return e.manual }

// SetManualStates replaces all manual override states.
func (e *Engine) SetManualStates(states [Zones]ManualState) {
	e.manual = states
	for z := 0; z < int(Zones); z++ {
		if e.manual[z].Enabled {
			e.cur[z] = rawWord(e.manual[z].TempRaw, e.manual[z].HumiRaw)
		}
	}
}

// SetManual freezes one zone. A zero until means "until explicitly released".
// It returns the raw register word that should be applied immediately.
func (e *Engine) SetManual(z Zone, tempRaw, humiRaw uint8, until time.Time) uint16 {
	z = clampZone(z)
	e.manual[z] = ManualState{Enabled: true, TempRaw: tempRaw, HumiRaw: humiRaw, Until: until}
	w := rawWord(tempRaw, humiRaw)
	e.cur[z] = w
	return w
}

// ClearManual releases one zone back to automatic control.
func (e *Engine) ClearManual(z Zone) { e.manual[clampZone(z)] = ManualState{} }

// ClearAllManual releases every zone back to automatic control.
func (e *Engine) ClearAllManual() { e.manual = [Zones]ManualState{} }

// ManualMask returns bit0=top, bit1=middle, bit2=bottom.
func (e *Engine) ManualMask() uint8 {
	var mask uint8
	for z := 0; z < int(Zones); z++ {
		if e.manual[z].Enabled {
			mask |= 1 << uint(z)
		}
	}
	return mask
}

// ExpireManual clears overrides whose deadline has passed. It reports whether
// anything changed.
func (e *Engine) ExpireManual(now time.Time) bool {
	changed := false
	for z := 0; z < int(Zones); z++ {
		m := e.manual[z]
		if m.Enabled && !m.Until.IsZero() && !now.Before(m.Until) {
			e.manual[z] = ManualState{}
			changed = true
		}
	}
	return changed
}

// ValidateParams checks the persisted climate configuration.
func ValidateParams(p Params) error {
	if p.UpdateMS < 100 {
		return fmt.Errorf("update_ms 不能小于 100")
	}
	if p.TimeScale <= 0 || p.TimeScale > 100000 {
		return fmt.Errorf("time_scale 必须在 (0,100000]")
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return fmt.Errorf("timezone %q 无法解析: %w", p.Timezone, err)
	}
	t := p.Temperature
	if !finite(t.Mean) || !finite(t.Amplitude) || !finite(t.PeakHour) ||
		!finite(t.Harmonic2Amplitude) || !finite(t.Min) || !finite(t.Max) {
		return fmt.Errorf("temperature 含有非有限数值")
	}
	if t.Min >= t.Max || t.Min < -40 || t.Max > 87.5 {
		return fmt.Errorf("temperature 范围非法")
	}
	if t.Amplitude < 0 || t.Harmonic2Amplitude < 0 {
		return fmt.Errorf("temperature 振幅不能为负")
	}
	h := p.Humidity
	if !finite(h.Mean) || !finite(h.TempCoupling) || !finite(h.Amplitude) || !finite(h.Min) || !finite(h.Max) {
		return fmt.Errorf("humidity 含有非有限数值")
	}
	if h.Min >= h.Max || h.Min < 0 || h.Max > 100 {
		return fmt.Errorf("humidity 范围非法")
	}
	if h.Amplitude < 0 {
		return fmt.Errorf("humidity 振幅不能为负")
	}
	for name, off := range map[string]ZoneOffset{
		"top": p.ZoneOffsets.Top, "mid": p.ZoneOffsets.Mid, "bottom": p.ZoneOffsets.Bottom,
	} {
		if !finite(off.Temp) || !finite(off.Humi) || !finite(off.LagH) {
			return fmt.Errorf("zone_offsets.%s 含有非有限数值", name)
		}
	}
	if p.Noise.TempTauMS <= 0 || p.Noise.HumiTauMS <= 0 {
		return fmt.Errorf("noise tau 必须为正数")
	}
	if !finite(p.Noise.TempSigma) || !finite(p.Noise.HumiSigma) ||
		p.Noise.TempSigma < 0 || p.Noise.HumiSigma < 0 {
		return fmt.Errorf("noise sigma 必须是非负有限数")
	}
	return nil
}

func finite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// Current returns the last computed raw register words.
func (e *Engine) Current() [Zones]uint16 { return e.cur }

// Tick advances the simulation, respecting the configured update interval.
// It reports whether the register values changed.
func (e *Engine) Tick(now time.Time) ([Zones]uint16, bool) {
	expired := e.ExpireManual(now)
	interval := time.Duration(e.params.UpdateMS) * time.Millisecond
	if !expired && now.Sub(e.lastUpdate) < interval {
		return e.cur, false
	}
	dt := now.Sub(e.lastReal)
	e.lastReal = now
	e.lastUpdate = now
	e.advanceNoise(dt)
	next := e.compute(now)
	changed := expired || next != e.cur
	e.cur = next
	return e.cur, changed
}

// Refresh recomputes immediately (used after manual overrides are changed).
func (e *Engine) Refresh(now time.Time) [Zones]uint16 {
	dt := now.Sub(e.lastReal)
	e.lastReal = now
	e.lastUpdate = now
	e.advanceNoise(dt)
	e.cur = e.compute(now)
	return e.cur
}

func (e *Engine) advanceNoise(dt time.Duration) {
	sec := dt.Seconds()
	if sec <= 0 {
		return
	}
	if sec > 60 {
		sec = 60
	}
	n := e.params.Noise
	e.commonTemp = ouStep(e.commonTemp, sec, float64(n.TempTauMS)/1000, n.TempSigma, e.rng)
	e.zoneTemp[0] = ouStep(e.zoneTemp[0], sec, float64(n.TempTauMS)/1000, n.TempSigma*0.5, e.rng)
	e.zoneTemp[1] = ouStep(e.zoneTemp[1], sec, float64(n.TempTauMS)/1000, n.TempSigma*0.5, e.rng)
	e.zoneTemp[2] = ouStep(e.zoneTemp[2], sec, float64(n.TempTauMS)/1000, n.TempSigma*0.5, e.rng)
	e.commonHumi = ouStep(e.commonHumi, sec, float64(n.HumiTauMS)/1000, n.HumiSigma, e.rng)
	e.zoneHumi[0] = ouStep(e.zoneHumi[0], sec, float64(n.HumiTauMS)/1000, n.HumiSigma*0.5, e.rng)
	e.zoneHumi[1] = ouStep(e.zoneHumi[1], sec, float64(n.HumiTauMS)/1000, n.HumiSigma*0.5, e.rng)
	e.zoneHumi[2] = ouStep(e.zoneHumi[2], sec, float64(n.HumiTauMS)/1000, n.HumiSigma*0.5, e.rng)
}

func ouStep(x, dt, tau, sigma float64, rng *rand.Rand) float64 {
	if tau <= 0 || sigma == 0 {
		return x
	}
	alpha := math.Exp(-dt / tau)
	return alpha*x + sigma*math.Sqrt(1-alpha*alpha)*rng.NormFloat64()
}

func (e *Engine) compute(now time.Time) [Zones]uint16 {
	var out [Zones]uint16
	p := e.params
	simNow := e.start.Add(time.Duration(float64(now.Sub(e.start)) * p.TimeScale))
	local := simNow.In(e.loc)
	h := float64(local.Hour()) + float64(local.Minute())/60 + float64(local.Second())/3600
	theta := 2 * math.Pi / 24
	offsets := [Zones]ZoneOffset{p.ZoneOffsets.Top, p.ZoneOffsets.Mid, p.ZoneOffsets.Bottom}

	for z := 0; z < int(Zones); z++ {
		if e.manual[z].Enabled {
			out[z] = rawWord(e.manual[z].TempRaw, e.manual[z].HumiRaw)
			continue
		}
		off := offsets[z]
		hz := h - off.LagH
		daily := p.Temperature.Amplitude*math.Cos(theta*(hz-p.Temperature.PeakHour)) +
			p.Temperature.Harmonic2Amplitude*math.Cos(2*theta*(hz-p.Temperature.PeakHour))
		tempNoise := 0.7*e.commonTemp + 0.5*e.zoneTemp[z]
		temp := p.Temperature.Mean + off.Temp + daily + tempNoise

		humiNoise := 0.7*e.commonHumi + 0.5*e.zoneHumi[z]
		humi := p.Humidity.Mean + off.Humi -
			p.Humidity.TempCoupling*(daily+tempNoise) -
			p.Humidity.Amplitude*math.Cos(theta*(h-p.Temperature.PeakHour)) +
			humiNoise

		temp = clampFloat(temp, p.Temperature.Min, p.Temperature.Max)
		humi = clampFloat(humi, p.Humidity.Min, p.Humidity.Max)
		out[z] = rawWord(QuantTemp(temp), QuantHumi(humi))
	}
	return out
}

// QuantTemp converts Celsius to the 8-bit raw value used on the wire.
func QuantTemp(t float64) uint8 {
	t = clampFloat(t, -40, 87.5)
	return clampU8(int(math.RoundToEven((t + 40) / 0.5)))
}

// QuantHumi converts percent RH to the 8-bit raw value used on the wire.
func QuantHumi(h float64) uint8 {
	h = clampFloat(h, 0, 100)
	return clampU8(int(math.RoundToEven(h / 0.5)))
}

// TempFromRaw decodes the temperature byte using the gateway formula.
func TempFromRaw(raw uint8) float64 { return float64(raw)*0.5 - 40 }

// HumiFromRaw decodes the humidity byte using the gateway formula.
func HumiFromRaw(raw uint8) float64 { return float64(raw) * 0.5 }

func rawWord(tempRaw, humiRaw uint8) uint16 {
	return uint16(tempRaw)<<8 | uint16(humiRaw)
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampU8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func clampZone(z Zone) Zone {
	if z < Top {
		return Top
	}
	if z > Bottom {
		return Bottom
	}
	return z
}
