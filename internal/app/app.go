// Package app wires configuration, runtime state, transports and the web
// console into the iadu-sim process.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zoneBen/longuv/internal/buildinfo"
	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/pty"
	"github.com/zoneBen/longuv/internal/transport"
)

// Options controls one process run.
type Options struct {
	ConfigPath string
	BaseDir    string
}

type runner interface {
	Start() error
	Stop()
}

// Run starts the simulator and blocks until the context is cancelled or a
// termination signal arrives.
func Run(ctx context.Context, opts Options) error {
	cfg, err := config.Load(opts.ConfigPath, opts.BaseDir)
	if err != nil {
		return err
	}
	logger := newLogger(cfg)
	ring := framelog.New(cfg.Server.Web.FrameBuffer)
	h, err := hub.New(cfg, ring, logger, nil)
	if err != nil {
		return fmt.Errorf("初始化运行状态: %w", err)
	}

	logger.Info("iadu-sim 启动",
		"version", buildinfo.Version, "commit", buildinfo.Commit,
		"config", opts.ConfigPath, "devices", len(cfg.Devices), "ports", len(cfg.Ports))

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	tickerDone := make(chan struct{})
	go func() {
		defer close(tickerDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				h.Tick(now)
			}
		}
	}()

	var runners []runner
	startedPorts := 0
	for _, port := range cfg.Ports {
		if port.Transport != "pty" {
			logger.Warn("暂不支持该端口承载，已跳过", "port", port.Name, "transport", port.Transport)
			continue
		}
		if !pty.IsSupported() {
			logger.Warn("当前平台不支持 pty，RTU 端口未启动", "port", port.Name)
			continue
		}
		r := transport.NewRTUPort(port, h, ring, logger)
		if err := r.Start(); err != nil {
			for _, started := range runners {
				started.Stop()
			}
			return err
		}
		runners = append(runners, r)
		startedPorts++
	}
	if startedPorts == 0 {
		logger.Warn("没有可用的 RTU 端口；仅运行管理接口（后续里程碑补全 Modbus TCP/Web）")
	}

	<-ctx.Done()
	logger.Info("收到退出信号，正在停止 iadu-sim")
	for _, r := range runners {
		r.Stop()
	}
	<-tickerDone
	logger.Info("iadu-sim 已停止")
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Server.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}
