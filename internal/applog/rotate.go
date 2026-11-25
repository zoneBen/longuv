// Package applog provides a small size-based log rotating writer.
package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RotatingWriter writes to a file and rotates it by size.
type RotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

// NewRotatingWriter opens (and creates if necessary) the log file.
func NewRotatingWriter(path string, maxSizeMB, maxBackups int) (*RotatingWriter, error) {
	if maxSizeMB <= 0 {
		maxSizeMB = 20
	}
	if maxBackups <= 0 {
		maxBackups = 5
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("创建日志目录 %s: %w", filepath.Dir(path), err)
	}
	w := &RotatingWriter{
		path:       path,
		maxBytes:   int64(maxSizeMB) << 20,
		maxBackups: maxBackups,
	}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("打开日志文件 %s: %w", w.path, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("读取日志文件大小: %w", err)
	}
	w.file = f
	w.size = st.Size()
	return nil
}

// Write implements io.Writer.
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) rotate() error {
	_ = w.file.Close()
	w.file = nil
	for i := w.maxBackups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", w.path, i)
		newPath := fmt.Sprintf("%s.%d", w.path, i+1)
		_ = os.Remove(newPath)
		_ = os.Rename(oldPath, newPath)
	}
	_ = os.Remove(w.path + ".1")
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		_ = w.open()
		return fmt.Errorf("轮转日志: %w", err)
	}
	w.size = 0
	return w.open()
}

// Close closes the current log file.
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}
