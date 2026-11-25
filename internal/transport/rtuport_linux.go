//go:build linux

package transport

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/pty"
)

// RTUPort owns one pty pair and its RTU read loop.
type RTUPort struct {
	cfg  config.Port
	hub  *hub.Hub
	ring *framelog.Ring
	log  *slog.Logger

	pair *pty.Pair
	stop chan struct{}
	done chan struct{}
	once sync.Once
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
	fail := func(err error) error {
		_ = pair.Master.Close()
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
	p.pair = pair
	p.hub.SetSlavePath(p.cfg.Name, pair.SlavePath)
	p.log.Info("RTU 端口已就绪",
		"port", p.cfg.Name, "link", p.cfg.Link, "pts", pair.SlavePath,
		"baud", p.cfg.Baud, "mode", p.cfg.Mode)
	go p.loop()
	return nil
}

// Stop closes the master, removes the stable link and waits for the loop.
func (p *RTUPort) Stop() {
	p.once.Do(func() {
		close(p.stop)
		if p.pair == nil {
			return
		}
		_ = p.pair.Master.Close()
		pty.RemoveLinkIfTarget(p.cfg.Link, p.pair.SlavePath)
		<-p.done
		p.hub.SetConnected(p.cfg.Name, false)
	})
}

func (p *RTUPort) loop() {
	defer close(p.done)
	feeder := NewFeeder(p.cfg, p.hub, p.ring, p.log)
	pending := make([]byte, 0, 1024)
	buf := make([]byte, 4096)

	for {
		n, err := p.pair.Master.Read(buf)
		if n > 0 {
			p.hub.SetConnected(p.cfg.Name, true)
			pending = append(pending, buf[:n]...)
			if out := feeder.Feed(&pending); len(out) > 0 {
				if !p.sleep(time.Duration(p.cfg.ResponseDelayMS) * time.Millisecond) {
					return
				}
				if _, werr := p.pair.Master.Write(out); werr != nil {
					p.log.Warn("RTU 写回应答失败", "port", p.cfg.Name, "err", werr)
				}
				if !p.sleep(time.Duration(p.cfg.TxSilenceMS) * time.Millisecond) {
					return
				}
			}
		}
		if err != nil {
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
			select {
			case <-p.stop:
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
		if p.stopped() {
			return
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
