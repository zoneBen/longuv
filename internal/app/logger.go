package app

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/zoneBen/longuv/internal/applog"
	"github.com/zoneBen/longuv/internal/config"
)

// newLogger builds a logger that writes to stdout and, when configured, a
// rotating file. A file error is returned but the console logger stays usable.
func newLogger(cfg *config.Config) (*slog.Logger, io.Closer, error) {
	level := new(slog.LevelVar)
	switch strings.ToLower(cfg.Server.Log.Level) {
	case "debug":
		level.Set(slog.LevelDebug)
	case "warn":
		level.Set(slog.LevelWarn)
	case "error":
		level.Set(slog.LevelError)
	default:
		level.Set(slog.LevelInfo)
	}

	writers := []io.Writer{os.Stdout}
	var closer io.Closer
	var fileErr error
	if cfg.Server.Log.File != "" {
		rw, err := applog.NewRotatingWriter(
			cfg.Server.Log.File, cfg.Server.Log.MaxSizeMB, cfg.Server.Log.MaxBackups)
		if err != nil {
			fileErr = err
		} else {
			writers = append(writers, rw)
			closer = rw
		}
	}

	out := io.MultiWriter(writers...)
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Server.Log.JSON {
		handler = slog.NewJSONHandler(out, opts)
	} else {
		handler = slog.NewTextHandler(out, opts)
	}
	return slog.New(handler), closer, fileErr
}
