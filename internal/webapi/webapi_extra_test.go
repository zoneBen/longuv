package webapi_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/webapi"
)

func TestReadEndpoints(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "GET", "/api/devices", nil, "")
	if r.Status != http.StatusOK {
		t.Fatalf("devices = %d", r.Status)
	}
	if devices := asSlice(t, decode(t, r.Data)); len(devices) != 1 {
		t.Fatalf("devices = %v", devices)
	}
	r = do(t, ts, "GET", "/api/ports", nil, "")
	ports := asMap(t, decode(t, r.Data))
	if len(asSlice(t, ports["ports"])) != 2 {
		t.Fatalf("ports = %v", ports["ports"])
	}
	if _, ok := ports["mbap"]; ok {
		t.Fatal("mbap stats should be omitted when the server is disabled")
	}
	r = do(t, ts, "GET", "/api/frames?limit=5&port=COM1", nil, "")
	frames := asMap(t, decode(t, r.Data))
	if _, ok := frames["frames"]; !ok {
		t.Fatalf("frames payload = %v", frames)
	}
	if r = do(t, ts, "GET", "/api/frames?limit=bad", nil, ""); r.Status != http.StatusOK {
		t.Fatalf("invalid limit = %d", r.Status)
	}
}

func TestPulseAndClearChanged(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "POST", "/api/devices/iadu-1/pulse-changed", map[string]any{"u": 3}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("pulse = %d %+v", r.Status, r.Error)
	}
	detail := asMap(t, decode(t, r.Data))
	if uint64(asFloat(t, asMap(t, detail["status"])["changed"]))&(1<<2) == 0 {
		t.Fatalf("changed mask = %v", asMap(t, detail["status"])["changed"])
	}
	r = do(t, ts, "POST", "/api/devices/iadu-1/pulse-changed", nil, "")
	if r.Status != http.StatusOK {
		t.Fatalf("pulse all = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	if got := asFloat(t, asMap(t, detail["status"])["changed"]); got != (1<<42)-1 {
		t.Fatalf("pulse all mask = %v", got)
	}
	if r = do(t, ts, "POST", "/api/devices/iadu-1/pulse-changed", map[string]any{"u": 99}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid pulse = %d", r.Status)
	}
	r = do(t, ts, "POST", "/api/devices/iadu-1/changed/clear", nil, "")
	if r.Status != http.StatusOK {
		t.Fatalf("clear changed = %d", r.Status)
	}
	detail = asMap(t, decode(t, r.Data))
	if got := asFloat(t, asMap(t, detail["status"])["changed"]); got != 0 {
		t.Fatalf("changed mask after clear = %v", got)
	}
}

func TestPatchDeviceFields(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "PATCH", "/api/devices/iadu-1", map[string]any{
		"name": "renamed", "esn": "NEW-ESN", "u_count": 30,
		"model": 0x9000, "baud_code": 4, "soft_version": 0x0203, "hard_version": 0x0304,
		"adopt_address": false, "strict": true, "changed_hold_ms": 1000,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("patch = %d %+v", r.Status, r.Error)
	}
	snap := asMap(t, asMap(t, decode(t, r.Data))["snapshot"])
	if asString(t, snap["name"]) != "renamed" || asString(t, snap["esn"]) != "NEW-ESN" {
		t.Fatalf("snapshot identity = %+v", snap)
	}
	if asFloat(t, snap["u_count"]) != 30 || asFloat(t, snap["model"]) != 0x9000 {
		t.Fatalf("snapshot shape = %+v", snap)
	}
	if asFloat(t, snap["baud_code"]) != 4 || asFloat(t, snap["soft_version"]) != 0x0203 ||
		asFloat(t, snap["hard_version"]) != 0x0304 {
		t.Fatalf("snapshot versions = %+v", snap)
	}
	if snap["adopt_address"] != false || snap["strict"] != true ||
		asFloat(t, snap["changed_hold_ms"]) != 1000 {
		t.Fatalf("snapshot flags = %+v", snap)
	}
	if r = do(t, ts, "PATCH", "/api/devices/iadu-1", map[string]any{"tcp_unit_id": 2}, ""); r.Status != http.StatusOK {
		t.Fatalf("patch tcp unit = %d", r.Status)
	}

	bad := []struct {
		name string
		body any
	}{
		{"empty name", map[string]any{"name": "  "}},
		{"long esn", map[string]any{"esn": strings.Repeat("x", 21)}},
		{"bad esn", map[string]any{"esn": "x\ny"}},
		{"bad u count", map[string]any{"u_count": 99}},
		{"bad tcp unit", map[string]any{"tcp_unit_id": 0}},
		{"bad changed hold", map[string]any{"changed_hold_ms": 0}},
	}
	for _, tc := range bad {
		if r := do(t, ts, "PATCH", "/api/devices/iadu-1", tc.body, ""); r.Status != http.StatusBadRequest &&
			r.Status != http.StatusConflict {
			t.Fatalf("%s = %d %+v", tc.name, r.Status, r.Error)
		}
	}
	if r := do(t, ts, "DELETE", "/api/devices/iadu-1", nil, ""); r.Status != http.StatusOK {
		t.Fatalf("delete = %d", r.Status)
	}
	if r := do(t, ts, "DELETE", "/api/devices/iadu-1", nil, ""); r.Status != http.StatusNotFound {
		t.Fatalf("second delete = %d", r.Status)
	}
}

func TestCreateDeviceErrors(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing id", map[string]any{"name": "x"}, http.StatusBadRequest},
		{"bad u count", map[string]any{"id": "x", "u_count": 99}, http.StatusBadRequest},
		{"bad occupancy", map[string]any{"id": "x", "occupancy": "0-2"}, http.StatusBadRequest},
		{"conflicting unit", map[string]any{"id": "x", "tcp_unit_id": 1}, http.StatusConflict},
		{"bad binding port", map[string]any{
			"id": "x", "tcp_unit_id": 5,
			"bindings": []any{map[string]any{"port": "COM9", "unit_id": 1}},
		}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if r := do(t, ts, "POST", "/api/devices", tc.body, ""); r.Status != tc.want {
			t.Fatalf("%s = %d %+v", tc.name, r.Status, r.Error)
		}
	}
	if r := do(t, ts, "GET", "/api/devices/x", nil, ""); r.Status != http.StatusNotFound {
		t.Fatalf("failed create should roll back the device: %d", r.Status)
	}
	if r := do(t, ts, "PATCH", "/api/devices/missing", map[string]any{"name": "x"}, ""); r.Status != http.StatusNotFound {
		t.Fatalf("patch missing device = %d", r.Status)
	}
}

func TestMutationErrorBranches(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/tags/1", map[string]any{"tag": "zz"}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("bad tag = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/tags/1", map[string]any{"tag": "0102030405060708", "present": false}, ""); r.Status != http.StatusOK {
		t.Fatalf("tag clear = %d", r.Status)
	}
	detail := asMap(t, decode(t, do(t, ts, "GET", "/api/devices/iadu-1", nil, "").Data))
	if uint64(asFloat(t, asMap(t, detail["snapshot"])["present"]))&1 != 0 {
		t.Fatal("present=false did not clear U1")
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/occupancy", map[string]any{"bits": "0-3"}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("bad occupancy = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/occupancy", map[string]any{}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("empty occupancy = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/occupancy", map[string]any{"u": 2, "present": false}, ""); r.Status != http.StatusOK {
		t.Fatalf("single occupancy = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/occupancy", map[string]any{"u": 99, "present": true}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid occupancy u = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/led/99", map[string]any{"value": 1}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid led = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/bus", map[string]any{"comm_ok": false}, ""); r.Status != http.StatusOK {
		t.Fatalf("partial bus = %d", r.Status)
	}
	detail = asMap(t, decode(t, do(t, ts, "GET", "/api/devices/iadu-1", nil, "").Data))
	bus := asSlice(t, asMap(t, detail["snapshot"])["bus"])
	if asFloat(t, bus[1]) != 0 || asFloat(t, bus[0]) != 1 {
		t.Fatalf("partial bus = %v", bus)
	}
}

func TestClimateAndBindingErrors(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/climate/override", map[string]any{
		"zone": "all", "temp": 28.0, "humi": 55.0,
	}, ""); r.Status != http.StatusOK {
		t.Fatalf("override all = %d %+v", r.Status, r.Error)
	}
	detail := asMap(t, decode(t, do(t, ts, "GET", "/api/devices/iadu-1", nil, "").Data))
	if asFloat(t, asMap(t, detail["status"])["manual_mask"]) != 7 {
		t.Fatalf("manual mask = %v", asMap(t, detail["status"])["manual_mask"])
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/climate/override", map[string]any{
		"zone": "top", "temp": 100.0,
	}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("out-of-range override = %d", r.Status)
	}
	if r := do(t, ts, "DELETE", "/api/devices/iadu-1/climate/override", nil, ""); r.Status != http.StatusOK {
		t.Fatalf("clear all overrides = %d", r.Status)
	}
	if r := do(t, ts, "DELETE", "/api/devices/iadu-1/climate/override?zone=side", nil, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid clear zone = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/climate", map[string]any{
		"enabled": false, "timezone": "UTC", "time_scale": 1, "update_ms": 5000,
		"temperature": map[string]any{
			"mean": 25.0, "amplitude": 1.0, "peak_hour": 15.0,
			"harmonic2_amplitude": 0.0, "min": 18.0, "max": 30.0,
		},
		"humidity": map[string]any{
			"mean": 50.0, "temp_coupling": 1.0, "amplitude": 1.0, "min": 30.0, "max": 70.0,
		},
		"zone_offsets": map[string]any{
			"top":    map[string]any{"temp": 0.0, "humi": 0.0, "lag_h": 0.0},
			"mid":    map[string]any{"temp": 0.0, "humi": 0.0, "lag_h": 0.0},
			"bottom": map[string]any{"temp": 0.0, "humi": 0.0, "lag_h": 0.0},
		},
		"noise": map[string]any{
			"temp_tau_ms": 1000, "temp_sigma": 0.0,
			"humi_tau_ms": 1000, "humi_sigma": 0.0,
		},
		"fixed": []float64{100.0, 50.0},
	}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid fixed TH = %d", r.Status)
	}
	if r := do(t, ts, "POST", "/api/bindings", map[string]any{
		"device": "iadu-1", "port": "COM1", "unit_id": 0,
	}, ""); r.Status != http.StatusConflict {
		t.Fatalf("invalid binding unit = %d", r.Status)
	}
	if r := do(t, ts, "DELETE", "/api/bindings", map[string]any{
		"device": "iadu-1", "port": "COM1", "unit_id": 42,
	}, ""); r.Status != http.StatusNotFound {
		t.Fatalf("missing binding = %d", r.Status)
	}
}

func TestPersistFailureSurfaces(t *testing.T) {
	cfg := config.Default(t.TempDir())
	disabled := false
	cfg.Devices[0].Climate.Enabled = &disabled
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := hub.New(cfg, framelog.New(16), log, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := webapi.New(webapi.Deps{
		Hub: h, Config: cfg, Ring: h.Ring(), Log: log,
		SaveNow: func() error { return io.ErrClosedPipe },
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	r := do(t, ts, "PUT", "/api/devices/iadu-1/led/1", map[string]any{"value": 1}, "")
	if r.Status != http.StatusInternalServerError || r.Error == nil || r.Error.Code != "save_failed" {
		t.Fatalf("save failure = %d %+v", r.Status, r.Error)
	}
}

func TestReadJSONStrictness(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	post := func(body string) int {
		req, err := http.NewRequest("PATCH", ts.URL+"/api/devices/iadu-1", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if status := post(`{"name":"x"}{"name":"y"}`); status != http.StatusBadRequest {
		t.Fatalf("trailing JSON = %d", status)
	}
	if status := post(`{"bogus":1}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", status)
	}
	if status := post(`{"name":"ok"}`); status != http.StatusOK {
		t.Fatalf("valid patch = %d", status)
	}
	big := `{"name":"` + strings.Repeat("x", 1<<20) + `"}`
	if status := post(big); status != http.StatusBadRequest {
		t.Fatalf("oversized body = %d", status)
	}
}

func TestCreateDeviceValidationBounds(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"tcp unit 255", map[string]any{"id": "x", "tcp_unit_id": 255}},
		{"esn control char", map[string]any{"id": "x", "esn": "bad\n"}},
		{"esn too long", map[string]any{"id": "x", "esn": strings.Repeat("x", 21)}},
		{"u count too large", map[string]any{"id": "x", "u_count": 43}},
	} {
		if r := do(t, ts, "POST", "/api/devices", tc.body, ""); r.Status != http.StatusBadRequest {
			t.Fatalf("%s = %d %+v", tc.name, r.Status, r.Error)
		}
	}
}

func TestPatchValidationIsAtomic(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "PATCH", "/api/devices/iadu-1", map[string]any{
		"name": "should-not-stick", "changed_hold_ms": 0,
	}, "")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("invalid patch = %d", r.Status)
	}
	detail := asMap(t, decode(t, do(t, ts, "GET", "/api/devices/iadu-1", nil, "").Data))
	if name := asString(t, asMap(t, detail["snapshot"])["name"]); name == "should-not-stick" {
		t.Fatal("name was applied even though the patch failed")
	}
	r = do(t, ts, "PATCH", "/api/devices/iadu-1", map[string]any{
		"name": "should-not-stick", "esn": "bad\n",
	}, "")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("invalid esn patch = %d", r.Status)
	}
	detail = asMap(t, decode(t, do(t, ts, "GET", "/api/devices/iadu-1", nil, "").Data))
	if name := asString(t, asMap(t, detail["snapshot"])["name"]); name == "should-not-stick" {
		t.Fatal("name was applied before a later invalid field")
	}
}

func TestTagClearWhilePresent(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "PUT", "/api/devices/iadu-1/tags/1", map[string]any{
		"tag": "0000000000000000", "present": true,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("clear tag = %d %+v", r.Status, r.Error)
	}
	detail := asMap(t, decode(t, r.Data))
	snap := asMap(t, detail["snapshot"])
	if tag := asString(t, asSlice(t, snap["tags"])[0]); tag != "0000000000000000" {
		t.Fatalf("cleared tag = %s", tag)
	}
	if uint64(asFloat(t, snap["present"]))&1 == 0 {
		t.Fatal("U1 should remain present")
	}
}
