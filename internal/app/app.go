// Package app wires configuration, runtime state, transports and the web
// console into the iadu-sim process.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zoneBen/longuv/internal/buildinfo"
	"github.com/zoneBen/longuv/internal/config"
	"github.com/zoneBen/longuv/internal/framelog"
	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/pty"
	"github.com/zoneBen/longuv/internal/state"
	"github.com/zoneBen/longuv/internal/transport"
	"github.com/zoneBen/longuv/internal/webapi"
)

// Options controls one process run.
type Options struct {
	ConfigPath        string
	BaseDir           string
	ImportConfig      bool
	AllowCorruptState bool
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
	logger, logCloser, logErr := newLogger(cfg)
	if logCloser != nil {
		defer func() { _ = logCloser.Close() }()
	}
	if logErr != nil {
		logger.Warn("日志文件不可用，仅输出到 stdout", "err", logErr)
	}

	ring := framelog.New(cfg.Server.Web.FrameBuffer)
	store := state.NewStore(cfg.Server.StateFile)

	var sv *saver
	onChange := func() {
		if sv != nil {
			sv.Trigger()
		}
	}
	h, seeded, err := buildHub(cfg, store, opts, ring, logger, onChange)
	if err != nil {
		return err
	}
	sv = newSaver(h, store, logger)
	if seeded {
		if err := sv.SaveNow(); err != nil {
			logger.Warn("初始状态保存失败", "path", store.Path(), "err", err)
		}
		logger.Info("已按配置播种设备状态", "state_file", store.Path())
	} else {
		logger.Info("已加载状态文件", "state_file", store.Path(), "devices", len(h.Devices()))
	}

	runCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	tickerDone := make(chan struct{})
	go climateTicker(runCtx, h, tickerDone)

	var runners []runner
	stopped := false
	cleanup := func() {
		if stopped {
			return
		}
		stopped = true
		stopSignals()
		for i := len(runners) - 1; i >= 0; i-- {
			runners[i].Stop()
		}
		_ = sv.Stop()
		<-tickerDone
	}

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
			cleanup()
			return err
		}
		runners = append(runners, r)
	}

	var mbap *transport.MBAPServer
	if cfg.Server.ModbusTCP.IsEnabled() {
		mbap = transport.NewMBAPServer(cfg.Server.ModbusTCP, h, ring, logger)
		if err := mbap.Start(); err != nil {
			cleanup()
			return err
		}
		runners = append(runners, mbap)
	}

	if cfg.Server.Web.IsEnabled() {
		web := webapi.New(webapi.Deps{
			Hub:        h,
			Config:     cfg,
			ConfigPath: opts.ConfigPath,
			Ring:       ring,
			MBAP:       mbap,
			Log:        logger,
			Version:    buildinfo.Version,
			Commit:     buildinfo.Commit,
			Started:    time.Now(),
			SaveNow:    sv.SaveNow,
		})
		if err := web.Start(); err != nil {
			cleanup()
			return err
		}
		runners = append(runners, web)
	}

	logger.Info("iadu-sim 已启动",
		"version", buildinfo.Version, "commit", buildinfo.Commit,
		"devices", len(h.Devices()), "state_file", store.Path())

	<-runCtx.Done()
	logger.Info("收到退出信号，正在停止 iadu-sim")
	cleanup()
	logger.Info("iadu-sim 已停止")
	return nil
}

func buildHub(cfg *config.Config, store *state.Store, opts Options, ring *framelog.Ring, logger *slog.Logger, onChange func()) (*hub.Hub, bool, error) {
	if opts.ImportConfig {
		h, err := hub.New(cfg, ring, logger, onChange)
		return h, true, err
	}
	st, err := store.Load()
	if err == nil {
		h, herr := hub.NewFromState(cfg, st, ring, logger, onChange)
		if herr == nil {
			return h, false, nil
		}
		if !opts.AllowCorruptState {
			return nil, false, fmt.Errorf(
				"加载状态 %s 失败: %w（使用 --import-config 或 --allow-corrupt-state 重新播种）",
				store.Path(), herr)
		}
		logger.Warn("状态不可用，按配置重新播种", "err", herr)
		h, nerr := hub.New(cfg, ring, logger, onChange)
		return h, true, nerr
	}
	if errors.Is(err, state.ErrNotExist) {
		h, nerr := hub.New(cfg, ring, logger, onChange)
		return h, true, nerr
	}
	if opts.AllowCorruptState {
		logger.Warn("状态文件损坏，按配置重新播种", "err", err)
		h, nerr := hub.New(cfg, ring, logger, onChange)
		return h, true, nerr
	}
	return nil, false, fmt.Errorf(
		"加载状态 %s: %w（使用 --allow-corrupt-state 或 --import-config 重新播种）",
		store.Path(), err)
}

func climateTicker(ctx context.Context, h *hub.Hub, done chan struct{}) {
	defer close(done)
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
}
