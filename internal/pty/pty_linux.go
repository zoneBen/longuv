//go:build linux

// Package pty creates the virtual serial pairs used by the RTU simulator.
// On non-Linux platforms it degrades to an unsupported stub.
package pty

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

// Pair is an open pty master plus the slave path handed to the collector.
type Pair struct {
	Master    *os.File
	SlavePath string
}

// Open allocates a pty pair via /dev/ptmx.
func Open() (*Pair, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("打开 /dev/ptmx: %w", err)
	}
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("解锁 pty (TIOCSPTLCK): %w", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("获取 pty 编号 (TIOCGPTN): %w", err)
	}
	return &Pair{
		Master:    os.NewFile(uintptr(fd), "ptmx"),
		SlavePath: fmt.Sprintf("/dev/pts/%d", n),
	}, nil
}

// ConfigureSlave sets raw 8N1 termios on the slave and applies ownership.
// gid < 0 leaves the group unchanged.
func ConfigureSlave(path string, mode os.FileMode, gid int) error {
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("打开 pty slave %s: %w", path, err)
	}
	defer f.Close()

	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		return fmt.Errorf("读取 termios: %w", err)
	}
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL | unix.B9600
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, t); err != nil {
		return fmt.Errorf("设置 termios: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		return fmt.Errorf("设置 pty 权限 %#o: %w", mode, err)
	}
	if gid >= 0 {
		if err := f.Chown(-1, gid); err != nil {
			return fmt.Errorf("设置 pty 属组 %d: %w", gid, err)
		}
	}
	return nil
}

// PointLink atomically points link at target. Existing symlinks are replaced.
// A non-symlink is only moved to .bak.<timestamp> when replaceNonSymlink is
// true; the default protects real /dev/ttyXXX nodes from accidental clobbering.
func PointLink(link, target string, replaceNonSymlink bool) error {
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return fmt.Errorf("创建软链目录: %w", err)
	}
	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			if !replaceNonSymlink {
				return fmt.Errorf("%s 已存在且不是符号链接，拒绝覆盖", link)
			}
			backup := link + ".bak." + time.Now().Format("20060102150405")
			if err := os.Rename(link, backup); err != nil {
				return fmt.Errorf("备份已存在的 %s: %w", link, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查软链 %s: %w", link, err)
	}
	tmp := link + ".tmp." + strconv.Itoa(os.Getpid())
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("创建临时软链: %w", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换软链 %s: %w", link, err)
	}
	return nil
}

// RemoveLinkIfTarget removes link only when it is a symlink pointing at target.
func RemoveLinkIfTarget(link, target string) {
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return
	}
	got, err := os.Readlink(link)
	if err == nil && got == target {
		_ = os.Remove(link)
	}
}

// IsSupported reports whether pty creation is available.
func IsSupported() bool { return true }
