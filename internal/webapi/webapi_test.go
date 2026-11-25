package webapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/webapi"
)

type apiResponse struct {
	Status int
	OK     bool
	Data   json.RawMessage
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
}

func newTestAPI(t *testing.T, token string) (*httptest.Server, *hub.Hub, *config.Config) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	disabled := false
	cfg.Devices[0].Climate.Enabled = &disabled
	cfg.Devices[0].Fixed = config.FixedTH{Temp: 26.5, Humi: 48.0}
	cfg.Devices[0].ESN = "iADU-SIM-00001"
	cfg.Server.Web.Token = token
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h, err := hub.New(cfg, framelog.New(128), log, nil)
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	srv := webapi.New(webapi.Deps{
		Hub: h, Config: cfg, ConfigPath: "/etc/iadu-sim/config.yaml",
		Ring: h.Ring(), Log: log, Version: "test", Commit: "deadbeef",
		Started: time.Now(), SaveNow: func() error { return nil },
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, h, cfg
}

func do(t *testing.T, ts *httptest.Server, method, path string, body any, token string) apiResponse {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, ts.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	return apiResponse{Status: resp.StatusCode, OK: env.OK, Data: env.Data, Error: env.Error}
}

func decode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode data: %v (%s)", err, string(raw))
	}
	return v
}

func asMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not a map: %T", v)
	}
	return m
}

func asSlice(t *testing.T, v any) []any {
	t.Helper()
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("not a slice: %T", v)
	}
	return s
}

func asString(t *testing.T, v any) string {
	t.Helper()
	s, ok := v.(string)
	if !ok {
		t.Fatalf("not a string: %T", v)
	}
	return s
}

func asFloat(t *testing.T, v any) float64 {
	t.Helper()
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("not a number: %T", v)
	}
	return f
}

func TestHealthSummaryAndDetail(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "GET", "/api/health", nil, "")
	if r.Status != http.StatusOK || !r.OK {
		t.Fatalf("health = %d %+v", r.Status, r.Error)
	}
	health := asMap(t, decode(t, r.Data))
	if asString(t, health["version"]) != "test" {
		t.Fatalf("version = %v", health["version"])
	}

	r = do(t, ts, "GET", "/api/summary", nil, "")
	summary := asMap(t, decode(t, r.Data))
	devices := asSlice(t, summary["devices"])
	if len(devices) != 1 {
		t.Fatalf("devices = %d", len(devices))
	}
	dev := asMap(t, devices[0])
	if asString(t, dev["id"]) != "iadu-1" || asFloat(t, dev["present_count"]) != 11 {
		t.Fatalf("device summary = %+v", dev)
	}
	top := asMap(t, asMap(t, dev["th"])["top"])
	if asFloat(t, top["temp"]) != 26.5 || asFloat(t, top["humi"]) != 48 {
		t.Fatalf("top TH = %+v", top)
	}
	if len(asSlice(t, summary["ports"])) != 2 {
		t.Fatalf("ports = %v", summary["ports"])
	}
	if asString(t, summary["config_path"]) != "/etc/iadu-sim/config.yaml" {
		t.Fatalf("config path = %v", summary["config_path"])
	}
	if asString(t, summary["state_file"]) == "" {
		t.Fatal("state file missing")
	}

	r = do(t, ts, "GET", "/api/devices/iadu-1", nil, "")
	detail := asMap(t, decode(t, r.Data))
	snap := asMap(t, detail["snapshot"])
	tags := asSlice(t, snap["tags"])
	if asString(t, tags[0]) != "E200000000000001" {
		t.Fatalf("U1 tag = %v", tags[0])
	}
	if asFloat(t, snap["u_count"]) != 42 {
		t.Fatalf("u_count = %v", snap["u_count"])
	}
	if len(asSlice(t, detail["bindings"])) != 2 {
		t.Fatalf("bindings = %v", detail["bindings"])
	}
	status := asMap(t, detail["status"])
	if asFloat(t, status["manual_mask"]) != 0 {
		t.Fatalf("manual mask = %v", status["manual_mask"])
	}
}

func TestDeviceCRUD(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	create := map[string]any{
		"id": "iadu-2", "name": "second", "u_count": 20, "esn": "TEST-2",
		"occupancy": "1-5", "tcp_unit_id": 2,
		"bindings": []any{map[string]any{"port": "COM1", "unit_id": 2}},
	}
	r := do(t, ts, "POST", "/api/devices", create, "")
	if r.Status != http.StatusCreated || !r.OK {
		t.Fatalf("create = %d %+v", r.Status, r.Error)
	}
	detail := asMap(t, decode(t, r.Data))
	snap := asMap(t, detail["snapshot"])
	if asFloat(t, snap["u_count"]) != 20 || asFloat(t, snap["present"]) != 31 {
		t.Fatalf("created snapshot = %+v", snap)
	}
	if len(asSlice(t, detail["bindings"])) != 1 {
		t.Fatalf("created bindings = %v", detail["bindings"])
	}

	if r = do(t, ts, "POST", "/api/devices", create, ""); r.Status != http.StatusConflict {
		t.Fatalf("duplicate create = %d", r.Status)
	}

	r = do(t, ts, "PATCH", "/api/devices/iadu-2", map[string]any{
		"name": "renamed", "tcp_unit_id": 9,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("patch = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	snap = asMap(t, detail["snapshot"])
	if asString(t, snap["name"]) != "renamed" || asFloat(t, snap["tcp_unit_id"]) != 9 {
		t.Fatalf("patched snapshot = %+v", snap)
	}

	if r = do(t, ts, "DELETE", "/api/devices/iadu-2", nil, ""); r.Status != http.StatusOK {
		t.Fatalf("delete = %d", r.Status)
	}
	if r = do(t, ts, "GET", "/api/devices/iadu-2", nil, ""); r.Status != http.StatusNotFound {
		t.Fatalf("get deleted = %d", r.Status)
	}
}

func TestDeviceMutations(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "PUT", "/api/devices/iadu-1/tags/2", map[string]any{
		"tag": "0102030405060708", "present": true,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("tag = %d %+v", r.Status, r.Error)
	}
	detail := asMap(t, decode(t, r.Data))
	snap := asMap(t, detail["snapshot"])
	if asString(t, asSlice(t, snap["tags"])[1]) != "0102030405060708" {
		t.Fatalf("tag not applied: %v", snap["tags"])
	}

	r = do(t, ts, "PUT", "/api/devices/iadu-1/occupancy", map[string]any{"bits": "1,3,5"}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("occupancy = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	snap = asMap(t, detail["snapshot"])
	if asFloat(t, snap["present"]) != 21 {
		t.Fatalf("present = %v", snap["present"])
	}

	r = do(t, ts, "PUT", "/api/devices/iadu-1/led/2", map[string]any{"value": 4660}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("led = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	snap = asMap(t, detail["snapshot"])
	if asFloat(t, asSlice(t, snap["led"])[1]) != 4660 {
		t.Fatalf("led = %v", snap["led"])
	}

	r = do(t, ts, "PUT", "/api/devices/iadu-1/bus", map[string]any{
		"connected": false, "comm_ok": true, "overcurrent": true,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("bus = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	snap = asMap(t, detail["snapshot"])
	bus := asSlice(t, snap["bus"])
	if asFloat(t, bus[0]) != 0 || asFloat(t, bus[1]) != 1 || asFloat(t, bus[2]) != 1 {
		t.Fatalf("bus = %v", bus)
	}
}

func TestClimateOverrideAndParams(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "PUT", "/api/devices/iadu-1/climate/override", map[string]any{
		"zone": "top", "temp": 30.0, "humi": 60.0, "duration_s": 60,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("override = %d %+v", r.Status, r.Error)
	}
	detail := asMap(t, decode(t, r.Data))
	top := asMap(t, asMap(t, detail["th"])["top"])
	if asFloat(t, top["temp"]) != 30 || asFloat(t, top["humi"]) != 60 {
		t.Fatalf("override top = %+v", top)
	}
	if asFloat(t, asMap(t, detail["status"])["manual_mask"]) != 1 {
		t.Fatalf("manual mask = %v", asMap(t, detail["status"])["manual_mask"])
	}

	r = do(t, ts, "DELETE", "/api/devices/iadu-1/climate/override?zone=top", nil, "")
	if r.Status != http.StatusOK {
		t.Fatalf("clear override = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	top = asMap(t, asMap(t, detail["th"])["top"])
	if asFloat(t, top["temp"]) != 26.5 || asFloat(t, top["humi"]) != 48 {
		t.Fatalf("restored top = %+v", top)
	}
	if asFloat(t, asMap(t, detail["status"])["manual_mask"]) != 0 {
		t.Fatalf("manual mask after clear = %v", asMap(t, detail["status"])["manual_mask"])
	}

	params := map[string]any{
		"enabled": false, "timezone": "UTC", "time_scale": 2, "update_ms": 5000,
		"temperature": map[string]any{
			"mean": 25.0, "amplitude": 1.6, "peak_hour": 15.0,
			"harmonic2_amplitude": 0.2, "min": 18.0, "max": 30.0,
		},
		"humidity": map[string]any{
			"mean": 50.0, "temp_coupling": 1.2, "amplitude": 1.5, "min": 30.0, "max": 70.0,
		},
		"zone_offsets": map[string]any{
			"top":    map[string]any{"temp": 0.8, "humi": -1.0, "lag_h": 0.25},
			"mid":    map[string]any{"temp": 0.0, "humi": 0.0, "lag_h": 0.0},
			"bottom": map[string]any{"temp": -0.6, "humi": 1.0, "lag_h": -0.25},
		},
		"noise": map[string]any{
			"temp_tau_ms": 180000, "temp_sigma": 0.15,
			"humi_tau_ms": 180000, "humi_sigma": 1.0,
		},
		"fixed": []float64{25.0, 55.0},
	}
	r = do(t, ts, "PUT", "/api/devices/iadu-1/climate", params, "")
	if r.Status != http.StatusOK {
		t.Fatalf("climate params = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	snap := asMap(t, detail["snapshot"])
	clim := asMap(t, snap["climate"])
	if asFloat(t, clim["time_scale"]) != 2 || asString(t, clim["timezone"]) != "UTC" {
		t.Fatalf("climate = %+v", clim)
	}
	fixed := asSlice(t, snap["fixed_th"])
	if asFloat(t, fixed[0]) != 25 || asFloat(t, fixed[1]) != 55 {
		t.Fatalf("fixed = %v", fixed)
	}
	top = asMap(t, asMap(t, detail["th"])["top"])
	if asFloat(t, top["temp"]) != 25 || asFloat(t, top["humi"]) != 55 {
		t.Fatalf("fixed TH = %+v", top)
	}
}

func TestBindingsAPI(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	r := do(t, ts, "POST", "/api/bindings", map[string]any{
		"device": "iadu-1", "port": "COM1", "unit_id": 5,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("add binding = %d %+v", r.Status, r.Error)
	}
	detail := asMap(t, decode(t, r.Data))
	if len(asSlice(t, detail["bindings"])) != 3 {
		t.Fatalf("bindings = %v", detail["bindings"])
	}
	if r = do(t, ts, "POST", "/api/bindings", map[string]any{
		"device": "iadu-1", "port": "COM1", "unit_id": 5,
	}, ""); r.Status != http.StatusConflict {
		t.Fatalf("duplicate binding = %d", r.Status)
	}
	r = do(t, ts, "DELETE", "/api/bindings", map[string]any{
		"device": "iadu-1", "port": "COM1", "unit_id": 5,
	}, "")
	if r.Status != http.StatusOK {
		t.Fatalf("remove binding = %d %+v", r.Status, r.Error)
	}
	detail = asMap(t, decode(t, r.Data))
	if len(asSlice(t, detail["bindings"])) != 2 {
		t.Fatalf("bindings after remove = %v", detail["bindings"])
	}
	if r = do(t, ts, "POST", "/api/bindings", map[string]any{
		"device": "missing", "port": "COM1", "unit_id": 6,
	}, ""); r.Status != http.StatusConflict {
		t.Fatalf("missing device binding = %d", r.Status)
	}
}

func TestAuthAndStatic(t *testing.T) {
	ts, _, _ := newTestAPI(t, "secret")
	if r := do(t, ts, "GET", "/api/health", nil, ""); r.Status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d", r.Status)
	}
	if r := do(t, ts, "GET", "/api/health", nil, "wrong"); r.Status != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", r.Status)
	}
	if r := do(t, ts, "GET", "/api/health", nil, "secret"); r.Status != http.StatusOK {
		t.Fatalf("authorized = %d", r.Status)
	}
	resp, err := http.Get(ts.URL + "/api/health?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query token = %d", resp.StatusCode)
	}

	resp2, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK || !strings.Contains(string(body), "iadu-sim") {
		t.Fatalf("static page = %d %q", resp2.StatusCode, string(body))
	}
	if !strings.Contains(resp2.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatal("missing CSP header")
	}
}

func TestSSEInitialSnapshot(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("sse dial: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sse status = %d", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("sse content type = %q", resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)
	seen := false
	for i := 0; i < 30; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		if strings.TrimSpace(line) != "event: snapshot" {
			continue
		}
		dataLine, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		payload := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(dataLine), "data:"))
		var summary map[string]any
		if err := json.Unmarshal([]byte(payload), &summary); err == nil {
			if _, ok := summary["devices"]; ok {
				seen = true
				break
			}
		}
	}
	if !seen {
		t.Fatal("did not receive a snapshot event")
	}
	cancel()
}

func TestErrorPaths(t *testing.T) {
	ts, _, _ := newTestAPI(t, "")
	if r := do(t, ts, "GET", "/api/devices/missing", nil, ""); r.Status != http.StatusNotFound {
		t.Fatalf("missing device = %d", r.Status)
	}
	if r := do(t, ts, "POST", "/api/devices", "not-json", ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid json = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/tags/99", map[string]any{"tag": ""}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid U = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/climate/override", map[string]any{"zone": "side"}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid zone = %d", r.Status)
	}
	if r := do(t, ts, "PUT", "/api/devices/iadu-1/climate", map[string]any{"enabled": false}, ""); r.Status != http.StatusBadRequest {
		t.Fatalf("invalid climate = %d", r.Status)
	}
	if r := do(t, ts, "GET", "/api/nope", nil, ""); r.Status != http.StatusNotFound {
		t.Fatalf("unknown api = %d", r.Status)
	}
	if r := do(t, ts, "GET", "/api/frames?limit=10", nil, ""); r.Status != http.StatusOK {
		t.Fatalf("frames = %d", r.Status)
	}
}
