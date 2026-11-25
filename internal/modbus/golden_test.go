package modbus_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/climate"
	"github.com/zoneBen/longuv/internal/modbus"
	"github.com/zoneBen/longuv/internal/model"
)

type goldenFile struct {
	Cases []struct {
		Name     string `json:"name"`
		Request  string `json:"request"`
		Response string `json:"response"`
	} `json:"cases"`
}

func TestGoldenFramesMatchPythonReference(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "rtu_golden.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var gf goldenFile
	if err := json.Unmarshal(data, &gf); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if len(gf.Cases) == 0 {
		t.Fatal("golden file has no cases")
	}

	dev := goldenDevice(t)
	slave := &modbus.Slave{Addr: 1, Bank: dev, BcastReplyOwn: true, AllowAdopt: false}

	for _, tc := range gf.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			req, err := hex.DecodeString(tc.Request)
			if err != nil {
				t.Fatalf("request hex: %v", err)
			}
			got := slave.Handle(req)
			if tc.Response == "" {
				if got != nil {
					t.Fatalf("expected no response, got %s", modbus.Hex(got))
				}
				return
			}
			want, err := hex.DecodeString(tc.Response)
			if err != nil {
				t.Fatalf("response hex: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("\n got: %s\nwant: %s", modbus.Hex(got), modbus.Hex(want))
			}
		})
	}
}

func goldenDevice(t *testing.T) *model.Device {
	t.Helper()
	const uCount = 42
	var present uint64
	for u := 1; u <= 10; u++ {
		present |= 1 << uint(u-1)
	}
	present |= 1 << 19 // U20

	seed, err := model.ParseTagHex("E200000000000000")
	if err != nil {
		t.Fatal(err)
	}
	tagBytes := model.GenerateTags(uCount, present, seed, 6, 2, nil)
	tags := make([]string, uCount)
	for i := range tagBytes {
		tags[i] = model.FormatTagHex(tagBytes[i])
	}
	snap := model.Snapshot{
		ID:            "iadu-1",
		Name:          "golden",
		UCount:        uCount,
		Model:         0x8020,
		BaudCode:      3,
		SoftVersion:   0x0100,
		HardVersion:   0x0100,
		ESN:           "iADU-SIM-00001",
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
	dev, err := model.New(snap, time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC), nil)
	if err != nil {
		t.Fatalf("new device: %v", err)
	}
	dev.SetPrimaryAddress(1)
	return dev
}
