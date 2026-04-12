package trace

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Logger writes structured trace lines to a file using slog.
// If not enabled, all methods are no-ops.
type Logger struct {
	slog    *slog.Logger
	file    *os.File
	enabled bool
}

// New creates a trace logger. If path is empty, tracing is disabled.
func New(path string) (*Logger, error) {
	if path == "" {
		return &Logger{}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("open trace file: %w", err)
	}
	h := slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
	return &Logger{
		slog:    slog.New(h),
		file:    f,
		enabled: true,
	}, nil
}

// Log writes a formatted trace line.
func (t *Logger) Log(format string, args ...any) {
	if !t.enabled {
		return
	}
	t.slog.Debug(fmt.Sprintf(format, args...))
}

// Writer returns an io.Writer for debug logging. Returns nil if disabled.
func (t *Logger) Writer() io.Writer {
	if !t.enabled {
		return nil
	}
	return t.file
}

// Enabled returns true if tracing is active.
func (t *Logger) Enabled() bool { return t.enabled }

// Close closes the trace file.
func (t *Logger) Close() {
	if t.file != nil {
		t.file.Close()
	}
}
