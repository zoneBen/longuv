package state

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/zoneBen/longuv/internal/model"
)

func TestSaveDefaultsAndOverride(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "nested", "state.json"))
	if err := store.Save(File{}); err != nil {
		t.Fatalf("Save zero value: %v", err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != CurrentVersion || got.SavedAt.IsZero() {
		t.Fatalf("defaults = version %d savedAt %v", got.Version, got.SavedAt)
	}
}

func TestSaveTemporaryFileError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.MkdirAll(filepath.Join(path+".tmp", "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	if err := store.Save(File{Version: CurrentVersion}); err == nil {
		t.Fatal("a directory blocking the temp file should fail the save")
	}
}

func TestSaveBackupError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path+".bak", "child"), 0o750); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	if err := store.Save(File{Version: CurrentVersion}); err == nil {
		t.Fatal("a blocked backup target should fail the save")
	}
}

func TestLoadDirectoryAndPath(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if store.Path() != dir {
		t.Fatalf("Path = %q", store.Path())
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("loading a directory should fail")
	}
}

func TestLoadMissingBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o640); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	if _, err := store.Load(); err == nil {
		t.Fatal("corrupt primary without backup should fail")
	}
}

func TestRoundTripPreservesDevices(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	in := File{
		Version: CurrentVersion,
		Devices: []model.Snapshot{{
			ID: "a", Name: "asset", UCount: 1, ESN: "E1",
			TCPUnitID: 1, Tags: []string{"0000000000000000"},
			Present: 1,
		}},
		Bindings: []model.Binding{{DeviceID: "a", Port: "COM1", UnitID: 1}},
	}
	if err := store.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Devices) != 1 || got.Devices[0].ID != "a" || got.Devices[0].Name != "asset" {
		t.Fatalf("devices = %+v", got.Devices)
	}
	if len(got.Bindings) != 1 || got.Bindings[0].Port != "COM1" {
		t.Fatalf("bindings = %+v", got.Bindings)
	}
}

func TestSaveMarshalError(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	nan := math.NaN()
	file := File{
		Version: CurrentVersion,
		Devices: []model.Snapshot{{ID: "nan", UCount: 1, FixedTH: [2]float64{nan, 50}}},
	}
	if err := store.Save(file); err == nil {
		t.Fatal("NaN in the state should fail JSON marshalling")
	}
}

func TestSaveDirectoryCreationError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewStore(filepath.Join(blocker, "state.json"))
	if err := store.Save(File{Version: CurrentVersion}); err == nil {
		t.Fatal("a file used as a directory should fail the save")
	}
}

func TestLoadFallsBackWhenPrimaryMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	first := File{Version: CurrentVersion, Devices: []model.Snapshot{{ID: "a", Name: "first", UCount: 1}}}
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	second := File{Version: CurrentVersion, Devices: []model.Snapshot{{ID: "a", Name: "second", UCount: 1}}}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(store.Path()); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil || got.Devices[0].Name != "first" {
		t.Fatalf("backup fallback = %+v, %v", got, err)
	}
	if err := os.Remove(store.Path() + ".bak"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrNotExist) {
		t.Fatalf("both missing should report ErrNotExist, got %v", err)
	}
}
