// Package state persists the runtime device/binding state to state.json.
// The YAML configuration is the process/port/seed description; once the state
// file exists it is authoritative for devices and bindings.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zoneBen/longuv/internal/model"
)

// CurrentVersion is the state schema version.
const CurrentVersion = 1

// ErrNotExist means no state file (or backup) exists yet.
var ErrNotExist = errors.New("状态文件不存在")

// File is the persisted runtime state.
type File struct {
	Version  int              `json:"version"`
	SavedAt  time.Time        `json:"saved_at"`
	Devices  []model.Snapshot `json:"devices"`
	Bindings []model.Binding  `json:"bindings"`
}

// Store reads and writes one state file with atomic replacement and a backup.
type Store struct {
	path string
}

// NewStore creates a store for path.
func NewStore(path string) *Store { return &Store{path: path} }

// Path returns the state file path.
func (s *Store) Path() string { return s.path }

// Load reads the state file. If the primary file is corrupt, it falls back to
// state.json.bak.
func (s *Store) Load() (File, error) {
	f, err := s.loadFile(s.path)
	if err == nil {
		return f, nil
	}
	// Always try the backup: Save renames the primary to .bak before
	// installing the new file, so a crash in that window leaves only .bak.
	bak, bakErr := s.loadFile(s.path + ".bak")
	if errors.Is(err, ErrNotExist) {
		if bakErr == nil {
			return bak, nil
		}
		if errors.Is(bakErr, ErrNotExist) {
			return File{}, ErrNotExist
		}
		return File{}, fmt.Errorf("状态文件缺失且备份不可用: %v", bakErr)
	}
	if bakErr == nil {
		return bak, nil
	}
	return File{}, fmt.Errorf("读取状态 %s: %w（备份不可用: %v）", s.path, err, bakErr)
}

// Save writes the state atomically and keeps the previous revision as .bak.
func (s *Store) Save(f File) error {
	if f.Version == 0 {
		f.Version = CurrentVersion
	}
	if f.Version != CurrentVersion {
		return fmt.Errorf("状态版本 %d 不受支持", f.Version)
	}
	if f.SavedAt.IsZero() {
		f.SavedAt = time.Now()
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化状态: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("创建状态目录 %s: %w", dir, err)
	}
	tmp := s.path + ".tmp"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fmt.Errorf("创建临时状态文件: %w", err)
	}
	if _, err := out.Write(data); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("写入临时状态文件: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("同步临时状态文件: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("关闭临时状态文件: %w", err)
	}

	backup := s.path + ".bak"
	if _, err := os.Stat(s.path); err == nil {
		_ = os.Remove(backup)
		if err := os.Rename(s.path, backup); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("备份旧状态: %w", err)
		}
		if err := os.Rename(tmp, s.path); err != nil {
			_ = os.Rename(backup, s.path)
			return fmt.Errorf("替换状态文件: %w", err)
		}
	} else if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("安装状态文件: %w", err)
	}

	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func (s *Store) loadFile(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return File{}, ErrNotExist
		}
		return File{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("解析 JSON: %w", err)
	}
	if f.Version != CurrentVersion {
		return File{}, fmt.Errorf("状态版本 %d 不受支持", f.Version)
	}
	return f, nil
}
