//go:build !linux

package transport

import (
	"log/slog"

	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/pty"
)

// RTUPort is a compile-time compatible stub on non-Linux platforms.
type RTUPort struct {
	cfg config.Port
}

// NewRTUPort constructs the unsupported stub.
func NewRTUPort(cfg config.Port, _ *hub.Hub, _ *framelog.Ring, _ *slog.Logger) *RTUPort {
	return &RTUPort{cfg: cfg}
}

// Start reports that pty transport is Linux-only.
func (p *RTUPort) Start() error { return pty.ErrUnsupported }

// Stop is a no-op.
func (p *RTUPort) Stop() {}
