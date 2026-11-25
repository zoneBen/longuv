package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunNoPorts(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `
version: 1
server:
  timezone: UTC
  web:
    listen: 127.0.0.1:18090
  modbus_tcp:
    enabled: true
    listen: 127.0.0.1:15020
ports: []
devices: []
bindings: []
`
	if err := os.WriteFile(configPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := Run(ctx, Options{ConfigPath: configPath, BaseDir: dir}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunSelftest(t *testing.T) {
	if err := RunSelftest(); err != nil {
		t.Fatalf("selftest: %v", err)
	}
}
