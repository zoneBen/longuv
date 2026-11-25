package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/state"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default(t.TempDir())
	disabled := false
	cfg.Devices[0].Climate.Enabled = &disabled
	return cfg
}

func TestBuildHubAllPaths(t *testing.T) {
	cfg := testConfig(t)
	store := state.NewStore(filepath.Join(t.TempDir(), "state.json"))
	ring := framelog.New(8)
	logger := discardLogger()

	h, seeded, err := buildHub(cfg, store, Options{}, ring, logger, nil)
	if err != nil || !seeded {
		t.Fatalf("missing state: seeded=%v err=%v", seeded, err)
	}
	if err := store.Save(h.SnapshotFile()); err != nil {
		t.Fatal(err)
	}
	_, seeded, err = buildHub(cfg, store, Options{}, ring, logger, nil)
	if err != nil || seeded {
		t.Fatalf("restored state: seeded=%v err=%v", seeded, err)
	}

	if err := os.WriteFile(store.Path(), []byte("{broken"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildHub(cfg, store, Options{}, ring, logger, nil); err == nil {
		t.Fatal("corrupt state should fail without permission")
	}
	if _, seeded, err := buildHub(cfg, store, Options{AllowCorruptState: true}, ring, logger, nil); err != nil || !seeded {
		t.Fatalf("corrupt state with allow: seeded=%v err=%v", seeded, err)
	}
	if _, seeded, err := buildHub(cfg, store, Options{ImportConfig: true}, ring, logger, nil); err != nil || !seeded {
		t.Fatalf("import config: seeded=%v err=%v", seeded, err)
	}

	if err := os.WriteFile(store.Path(), []byte(`{"version":99}`), 0o640); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(store.Path() + ".bak")
	if _, _, err := buildHub(cfg, store, Options{}, ring, logger, nil); err == nil {
		t.Fatal("unsupported state version should fail")
	}
	if _, seeded, err := buildHub(cfg, store, Options{AllowCorruptState: true}, ring, logger, nil); err != nil || !seeded {
		t.Fatalf("unsupported version with allow: seeded=%v err=%v", seeded, err)
	}

	invalid := `{"version":1,"devices":[{"id":"bad","u_count":0}],"bindings":[]}`
	if err := os.WriteFile(store.Path(), []byte(invalid), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildHub(cfg, store, Options{}, ring, logger, nil); err == nil {
		t.Fatal("invalid persisted snapshot should fail")
	}
	if _, seeded, err := buildHub(cfg, store, Options{AllowCorruptState: true}, ring, logger, nil); err != nil || !seeded {
		t.Fatalf("invalid snapshot with allow: seeded=%v err=%v", seeded, err)
	}
}

func TestSaverDebounceAndFailure(t *testing.T) {
	cfg := testConfig(t)
	dir := t.TempDir()
	logger := discardLogger()
	h, err := hub.New(cfg, framelog.New(8), logger, nil)
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(filepath.Join(dir, "state.json"))
	sv := newSaver(h, store, logger)
	dev, _ := h.Device("iadu-1")
	dev.SetName("debounced")
	sv.Trigger()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(store.Path()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("debounced save did not run")
		}
		time.Sleep(50 * time.Millisecond)
	}
	file, err := store.Load()
	if err != nil || file.Devices[0].Name != "debounced" {
		t.Fatalf("saved state = %+v, %v", file, err)
	}
	dev.SetName("sync")
	if err := sv.SaveNow(); err != nil {
		t.Fatalf("SaveNow: %v", err)
	}
	if err := sv.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := sv.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	sv.Trigger() // after Stop it must not block

	failPath := filepath.Join(dir, "fail-state")
	if err := os.WriteFile(failPath, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(failPath+".bak", "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	bad := newSaver(h, state.NewStore(failPath), logger)
	if err := bad.SaveNow(); err == nil {
		t.Fatal("SaveNow should fail when the backup target blocks the rename")
	}
	_ = bad.Stop()
}

func TestNewLoggerLevelsAndFiles(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		level string
		json  bool
	}{
		{"debug", false},
		{"warn", false},
		{"error", false},
		{"info", true},
		{"weird", false},
	} {
		cfg := config.Default(dir)
		cfg.Server.Log.Level = tc.level
		cfg.Server.Log.JSON = tc.json
		cfg.Server.Log.File = filepath.Join(dir, "logs", tc.level+".log")
		logger, closer, err := newLogger(cfg)
		if err != nil {
			t.Fatalf("newLogger(%s): %v", tc.level, err)
		}
		if closer == nil {
			t.Fatalf("newLogger(%s): missing file closer", tc.level)
		}
		logger.Info("hello", "level", tc.level)
		if err := closer.Close(); err != nil {
			t.Fatalf("close %s: %v", tc.level, err)
		}
	}

	cfg := config.Default(dir)
	cfg.Server.Log.File = dir
	logger, closer, err := newLogger(cfg)
	if err == nil || closer != nil || logger == nil {
		t.Fatalf("directory log target: logger=%v closer=%v err=%v", logger, closer, err)
	}
	cfg.Server.Log.File = ""
	logger, closer, err = newLogger(cfg)
	if err != nil || closer != nil || logger == nil {
		t.Fatalf("console-only logger: %v %v %v", logger, closer, err)
	}
}

func TestLoggerWritesJSONAndText(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default(dir)
	cfg.Server.Log.File = filepath.Join(dir, "json.log")
	cfg.Server.Log.JSON = true
	logger, closer, err := newLogger(cfg)
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("json-line", "k", "v")
	_ = closer.Close()
	data, err := os.ReadFile(cfg.Server.Log.File)
	if err != nil || !strings.HasPrefix(string(data), "{") {
		t.Fatalf("json log = %q, %v", data, err)
	}
}

func TestClimateTickerStops(t *testing.T) {
	cfg := testConfig(t)
	h, err := hub.New(cfg, framelog.New(8), discardLogger(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go climateTicker(ctx, h, done)
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("climate ticker did not stop")
	}
}

func TestRunConfigErrors(t *testing.T) {
	dir := t.TempDir()
	if err := Run(context.Background(), Options{ConfigPath: filepath.Join(dir, "missing.yaml"), BaseDir: dir}); err == nil {
		t.Fatal("missing config should fail")
	}
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("version: 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), Options{ConfigPath: bad, BaseDir: dir}); err == nil {
		t.Fatal("invalid config should fail")
	}
}

func TestRunSkipsUnsupportedPorts(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	yaml := `
version: 1
server:
  timezone: UTC
  web:
    enabled: false
    listen: 127.0.0.1:18090
  modbus_tcp:
    enabled: false
    listen: 127.0.0.1:15020
  state_file: ` + filepath.ToSlash(filepath.Join(dir, "state.json")) + `
  log:
    file: ` + filepath.ToSlash(filepath.Join(dir, "app.log")) + `
ports:
  - {name: COM1, transport: pty, link: ` + filepath.ToSlash(filepath.Join(dir, "com1")) + `}
  - {name: SER1, transport: serial, device: /dev/ttyS0, baud: 9600}
devices: []
bindings: []
`
	if err := os.WriteFile(configPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := Run(ctx, Options{ConfigPath: configPath, BaseDir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); err != nil {
		t.Fatalf("state file not written: %v", err)
	}
}

func TestRunWebStartFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	yaml := `
version: 1
server:
  timezone: UTC
  web:
    enabled: true
    listen: ` + ln.Addr().String() + `
  modbus_tcp:
    enabled: false
    listen: 127.0.0.1:15020
  state_file: ` + filepath.ToSlash(filepath.Join(dir, "state.json")) + `
  log:
    file: ` + filepath.ToSlash(filepath.Join(dir, "app.log")) + `
ports: []
devices: []
bindings: []
`
	if err := os.WriteFile(configPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), Options{ConfigPath: configPath, BaseDir: dir}); err == nil {
		t.Fatal("occupied web listen should fail the run")
	}
}

func TestRunMBAPStartFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	yaml := `
version: 1
server:
  timezone: UTC
  web:
    enabled: false
    listen: 127.0.0.1:18090
  modbus_tcp:
    enabled: true
    listen: ` + ln.Addr().String() + `
  state_file: ` + filepath.ToSlash(filepath.Join(dir, "state.json")) + `
  log:
    file: ` + filepath.ToSlash(filepath.Join(dir, "app.log")) + `
ports: []
devices: []
bindings: []
`
	if err := os.WriteFile(configPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), Options{ConfigPath: configPath, BaseDir: dir}); err == nil {
		t.Fatal("occupied Modbus TCP listen should fail the run")
	}
}
