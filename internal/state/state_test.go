package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zoneBen/longuv/internal/model"
)

func TestSaveLoadBackup(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	if _, err := store.Load(); !errors.Is(err, ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
	f1 := File{
		Version:  CurrentVersion,
		Devices:  []model.Snapshot{{ID: "a", Name: "first", UCount: 1, Tags: []string{"0000000000000000"}}},
		Bindings: []model.Binding{{DeviceID: "a", Port: "COM1", UnitID: 1}},
	}
	if err := store.Save(f1); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(store.Path() + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup after first save: %v", err)
	}

	f2 := File{
		Version:  CurrentVersion,
		Devices:  []model.Snapshot{f1.Devices[0]},
		Bindings: f1.Bindings,
	}
	f2.Devices[0].Name = "second"
	if err := store.Save(f2); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if _, err := os.Stat(store.Path() + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	got, err := store.Load()
	if err != nil || got.Devices[0].Name != "second" {
		t.Fatalf("Load = %+v, %v", got, err)
	}

	if err := os.WriteFile(store.Path(), []byte("{broken"), 0o640); err != nil {
		t.Fatal(err)
	}
	got, err = store.Load()
	if err != nil || got.Devices[0].Name != "first" {
		t.Fatalf("backup fallback = %+v, %v", got, err)
	}

	if err := os.WriteFile(store.Path()+".bak", []byte("{broken"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("both corrupt files should fail")
	}
}

func TestVersionValidation(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(filepath.Join(dir, "state.json"))
	if err := store.Save(File{Version: 99}); err == nil {
		t.Fatal("unsupported version accepted on save")
	}
	if err := os.WriteFile(store.Path(), []byte(`{"version":99}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("unsupported version accepted on load")
	}
}
