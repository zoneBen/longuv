//go:build !linux

package pty

import (
	"errors"
	"os"
)

// ErrUnsupported is returned on platforms without /dev/ptmx support.
var ErrUnsupported = errors.New("当前平台不支持 pty，仅限 Linux")

// Pair mirrors the Linux type so callers compile on every platform.
type Pair struct {
	Master    *os.File
	SlavePath string
}

// Open is unsupported outside Linux.
func Open() (*Pair, error) { return nil, ErrUnsupported }

// ConfigureSlave is unsupported outside Linux.
func ConfigureSlave(string, os.FileMode, int) error { return ErrUnsupported }

// PointLink is unsupported outside Linux.
func PointLink(string, string, bool) error { return ErrUnsupported }

// RemoveLinkIfTarget is a no-op outside Linux.
func RemoveLinkIfTarget(string, string) {}

// IsSupported reports whether pty creation is available.
func IsSupported() bool { return false }
