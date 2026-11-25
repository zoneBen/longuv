//go:build linux

package transport

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/pty"
)

var errPortStopped = errors.New("rtu port stopped")

// RTUPort owns one pty pair and its RTU read loop.
type RTUPort struct {
	cfg  config.Port
	hub  *hub.Hub
	ring *framelog.Ring
	log  *slog.Logger

	pair     *pty.Pair
	masterFD int
	stopR    *os.File
	stopW    *os.File
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

// NewRTUPort constructs an unstarted pty port.
func NewRTUPort(cfg config.Port, h *hub.Hub, ring *framelog.Ring, log *slog.Logger) *RTUPort {
	if log == nil {
		log = slog.Default()
	}
	return &RTUPort{
		cfg:  cfg,
		hub:  h,
		ring: ring,
		log:  log,
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
}

// Start allocates the pty, configures the slave and begins serving frames.
func (p *RTUPort) Start() error {
	if p.cfg.Transport != "pty" {
		return fmt.Errorf("端口 %s 的 transport %q 尚未实现（当前仅支持 pty）", p.cfg.Name, p.cfg.Transport)
	}
	pair, err := pty.Open()
	if err != nil {
		return fmt.Errorf("端口 %s: %w", p.cfg.Name, err)
	}
	var pipeFDs [2]int
	if err := unix.Pipe2(pipeFDs[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		_ = pair.Master.Close()
		return fmt.Errorf("端口 %s 创建停止管道: %w", p.cfg.Name, err)
	}
	stopR := os.NewFile(uintptr(pipeFDs[0]), "rtu-stop-r")
	stopW := os.NewFile(uintptr(pipeFDs[1]), "rtu-stop-w")
	fail := func(err error) error {
		_ = pair.Master.Close()
		_ = stopR.Close()
		_ = stopW.Close()
		return fmt.Errorf("端口 %s: %w", p.cfg.Name, err)
	}
	mode, err := config.ParseFileMode(p.cfg.Mode)
	if err != nil {
		return fail(err)
	}
	gid := -1
	if p.cfg.Group != "" {
		gid, err = lookupGID(p.cfg.Group)
		if err != nil {
			return fail(err)
		}
	}
	if err := pty.ConfigureSlave(pair.SlavePath, mode, gid); err != nil {
		return fail(err)
	}
	if err := pty.PointLink(p.cfg.Link, pair.SlavePath, p.cfg.ReplaceExisting); err != nil {
		return fail(err)
	}
	masterFD := int(pair.Master.Fd())
	if err := unix.SetNonblock(masterFD, true); err != nil {
		return fail(err)
	}
	p.pair = pair
	p.masterFD = masterFD
	p.stopR = stopR
	p.stopW = stopW
	p.hub.SetSlavePath(p.cfg.Name, pair.SlavePath)
	p.log.Info("RTU 端口已就绪",
		"port", p.cfg.Name, "link", p.cfg.Link, "pts", pair.SlavePath,
		"baud", p.cfg.Baud, "mode", p.cfg.Mode)
	go p.loop()
	return nil
}

// Stop wakes the poll loop, waits for it, then closes the master and removes
// the stable link. It never blocks on a collector that still holds the slave:
// the loop only waits in poll(2), and the stop pipe makes that return.
func (p *RTUPort) Stop() {
	p.once.Do(func() {
		close(p.stop)
		if p.stopW != nil {
			_ = p.stopW.Close()
		}
		if p.pair == nil {
			if p.stopR != nil {
				_ = p.stopR.Close()
			}
			return
		}
		<-p.done
		_ = p.pair.Master.Close()
		pty.RemoveLinkIfTarget(p.cfg.Link, p.pair.SlavePath)
		if p.stopR != nil {
			_ = p.stopR.Close()
		}
		p.hub.SetConnected(p.cfg.Name, false)
	})
}

func (p *RTUPort) loop() {
	defer close(p.done)
	feeder := NewFeeder(p.cfg, p.hub, p.ring, p.log)
	pending := make([]byte, 0, 1024)
	buf := make([]byte, 4096)
	stopFD := int32(p.stopR.Fd())
	fds := []unix.PollFd{
		{Fd: int32(p.masterFD), Events: unix.POLLIN},
		{Fd: stopFD, Events: unix.POLLIN},
	}

	for {
		fds[0].Revents = 0
		fds[1].Revents = 0
		if _, err := unix.Poll(fds, -1); err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if p.stopped() {
				return
			}
			p.log.Warn("RTU poll 失败", "port", p.cfg.Name, "err", err)
			if !p.sleep(200 * time.Millisecond) {
				return
			}
			continue
		}
		if fds[1].Revents != 0 {
			return
		}
		if fds[0].Revents == 0 {
			continue
		}

		n, err := unix.Read(p.masterFD, buf)
		if n > 0 {
			p.hub.SetConnected(p.cfg.Name, true)
			pending = append(pending, buf[:n]...)
			if out := feeder.Feed(&pending); len(out) > 0 {
				if !p.sleep(time.Duration(p.cfg.ResponseDelayMS) * time.Millisecond) {
					return
				}
				if werr := p.writeAll(out); werr != nil {
					if p.stopped() || errors.Is(werr, errPortStopped) {
						return
					}
					p.log.Warn("RTU 写回应答失败", "port", p.cfg.Name, "err", werr)
				}
				if !p.sleep(time.Duration(p.cfg.TxSilenceMS) * time.Millisecond) {
					return
				}
			}
		}
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) ||
				errors.Is(err, syscall.EWOULDBLOCK) {
				continue
			}
			if p.stopped() {
				return
			}
			if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
				p.hub.SetConnected(p.cfg.Name, false)
				pending = pending[:0]
				if !p.sleep(50 * time.Millisecond) {
					return
				}
				continue
			}
			p.log.Warn("RTU 读取失败", "port", p.cfg.Name, "err", err)
			if !p.sleep(200 * time.Millisecond) {
				return
			}
			continue
		}
		if n == 0 {
			// pty hangup without an explicit error.
			p.hub.SetConnected(p.cfg.Name, false)
			pending = pending[:0]
			if !p.sleep(50 * time.Millisecond) {
				return
			}
		}
	}
}

func (p *RTUPort) writeAll(data []byte) error {
	for len(data) > 0 {
		n, err := unix.Write(p.masterFD, data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				if werr := p.waitWritable(); werr != nil {
					return werr
				}
				continue
			}
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (p *RTUPort) waitWritable() error {
	fds := []unix.PollFd{
		{Fd: int32(p.masterFD), Events: unix.POLLOUT},
		{Fd: int32(p.stopR.Fd()), Events: unix.POLLIN},
	}
	for {
		fds[0].Revents = 0
		fds[1].Revents = 0
		if _, err := unix.Poll(fds, -1); err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return err
		}
		if fds[1].Revents != 0 {
			return errPortStopped
		}
		if fds[0].Revents != 0 {
			return nil
		}
	}
}

func (p *RTUPort) stopped() bool {
	select {
	case <-p.stop:
		return true
	default:
		return false
	}
}

// sleep waits for d but returns false immediately when Stop is requested.
func (p *RTUPort) sleep(d time.Duration) bool {
	if d <= 0 {
		return !p.stopped()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-p.stop:
		return false
	case <-t.C:
		return true
	}
}

func lookupGID(group string) (int, error) {
	if n, err := strconv.Atoi(group); err == nil {
		if n < 0 {
			return -1, fmt.Errorf("用户组 id %d 非法", n)
		}
		return n, nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return -1, fmt.Errorf("查找用户组 %q: %w", group, err)
	}
	n, err := strconv.Atoi(g.Gid)
	if err != nil {
		return -1, fmt.Errorf("用户组 %q 的 gid %q 非法: %w", group, g.Gid, err)
	}
	return n, nil
}
