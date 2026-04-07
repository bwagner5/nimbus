package trace

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

// Logger writes timestamped trace lines to a file. Safe for concurrent use.
// If not enabled, all methods are no-ops.
type Logger struct {
	mu      sync.Mutex
	logger  *log.Logger
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
	return &Logger{
		logger:  log.New(f, "", 0),
		file:    f,
		enabled: true,
	}, nil
}

// Log writes a formatted trace line with a timestamp.
func (t *Logger) Log(format string, args ...any) {
	if !t.enabled {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ts := time.Now().Format("15:04:05.000")
	t.logger.Printf("%s  %s", ts, fmt.Sprintf(format, args...))
}

// Writer returns an io.Writer for bubbletea's WithOutput debug logging.
// Returns nil if tracing is disabled.
func (t *Logger) Writer() io.Writer {
	if !t.enabled {
		return nil
	}
	return t.file
}

// Close closes the trace file.
func (t *Logger) Close() {
	if t.file != nil {
		t.file.Close()
	}
}
