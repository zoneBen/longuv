package hub

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zoneBen/longuv/internal/climate"
	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/model"
)

// SnapshotFromConfig converts a YAML device seed into the runtime snapshot
// used by model.New.
func SnapshotFromConfig(d config.Device) (model.Snapshot, error) {
	present, err := config.ParseOccupancy(d.Occupancy, d.UCount)
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("设备 %s 的 occupancy: %w", d.ID, err)
	}
	seed, err := config.ParseSeed(d.Tags.Seed)
	if err != nil {
		return model.Snapshot{}, fmt.Errorf("设备 %s 的 tags.seed: %w", d.ID, err)
	}
	custom := make(map[int][8]byte, len(d.Tags.Custom))
	for key, raw := range d.Tags.Custom {
		u, err := strconv.Atoi(strings.TrimSpace(key))
		if err != nil {
			return model.Snapshot{}, fmt.Errorf("设备 %s 的 tags.custom 键 %q 非法", d.ID, key)
		}
		tag, err := config.ParseTagHex(raw)
		if err != nil {
			return model.Snapshot{}, fmt.Errorf("设备 %s 的 tags.custom[%s]: %w", d.ID, key, err)
		}
		custom[u] = tag
	}
	tagBytes := model.GenerateTags(d.UCount, present, seed, d.Tags.IndexOffset, d.Tags.IndexBytes, custom)
	tagHex := make([]string, len(tagBytes))
	for i := range tagBytes {
		tagHex[i] = model.FormatTagHex(tagBytes[i])
	}
	connected, commOK, overcurrent := d.Bus.Values()
	return model.Snapshot{
		ID:            d.ID,
		Name:          d.Name,
		UCount:        d.UCount,
		Model:         d.Model,
		BaudCode:      d.BaudCode,
		SoftVersion:   d.SoftVersion,
		HardVersion:   d.HardVersion,
		ESN:           d.ESN,
		AdoptAddress:  d.AdoptsAddress(),
		ChangedHoldMS: d.ChangedHoldMS,
		TCPUnitID:     d.TCPUnitID,
		Strict:        d.Strict,
		Tags:          tagHex,
		Present:       present,
		Bus:           [3]uint16{btoi(connected), btoi(commOK), btoi(overcurrent)},
		LED:           make([]uint16, d.UCount),
		Climate:       climateFromConfig(d.Climate),
		FixedTH:       [2]float64{d.Fixed.Temp, d.Fixed.Humi},
	}, nil
}

func climateFromConfig(c config.Climate) climate.Params {
	return climate.Params{
		Enabled:   c.IsEnabled(),
		Timezone:  c.Timezone,
		TimeScale: c.TimeScale,
		UpdateMS:  c.UpdateMS,
		Temperature: climate.Temperature{
			Mean:               c.Temperature.Mean,
			Amplitude:          c.Temperature.Amplitude,
			PeakHour:           c.Temperature.PeakHour,
			Harmonic2Amplitude: c.Temperature.Harmonic2Amplitude,
			Min:                c.Temperature.Min,
			Max:                c.Temperature.Max,
		},
		Humidity: climate.Humidity{
			Mean:         c.Humidity.Mean,
			TempCoupling: c.Humidity.TempCoupling,
			Amplitude:    c.Humidity.Amplitude,
			Min:          c.Humidity.Min,
			Max:          c.Humidity.Max,
		},
		ZoneOffsets: climate.ZoneOffsets{
			Top: climate.ZoneOffset{
				Temp: c.ZoneOffsets.Top.Temp,
				Humi: c.ZoneOffsets.Top.Humi,
				LagH: c.ZoneOffsets.Top.LagH,
			},
			Mid: climate.ZoneOffset{
				Temp: c.ZoneOffsets.Mid.Temp,
				Humi: c.ZoneOffsets.Mid.Humi,
				LagH: c.ZoneOffsets.Mid.LagH,
			},
			Bottom: climate.ZoneOffset{
				Temp: c.ZoneOffsets.Bottom.Temp,
				Humi: c.ZoneOffsets.Bottom.Humi,
				LagH: c.ZoneOffsets.Bottom.LagH,
			},
		},
		Noise: climate.Noise{
			TempTauMS: c.Noise.TempTauMS,
			TempSigma: c.Noise.TempSigma,
			HumiTauMS: c.Noise.HumiTauMS,
			HumiSigma: c.Noise.HumiSigma,
		},
	}
}

func btoi(v bool) uint16 {
	if v {
		return 1
	}
	return 0
}
