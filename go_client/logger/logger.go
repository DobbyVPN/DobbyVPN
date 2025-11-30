package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
)

type Logger struct {
	file   *os.File
	logger *slog.Logger
	path   string
}

var logger *Logger = &Logger{}
var initMu sync.Mutex

func SetPath(path string) error {
	if logger.file != nil {
		return nil
	}
	initMu.Lock()
	defer initMu.Unlock()
	if logger.file != nil {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open log file: %w", err)
	}

	h := &customHandler{file: file}

	logger.file = file
	logger.path = path
	logger.logger = slog.New(h)

	return nil
}

func Infof(format string, args ...any) {
	if logger.logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	logger.logger.Info(msg)
}

type customHandler struct {
	file *os.File
}

func (h *customHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *customHandler) Handle(_ context.Context, r slog.Record) error {
	t := r.Time.Format("2006-01-02 15:04:05")

	msg := r.Message

	var src string
	if r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		src = fmt.Sprintf("%s:%d", f.File, f.Line)
	}

	_, err := fmt.Fprintf(h.file,
		"[%s] \"%s\" source=%s\n",
		t, msg, src,
	)

	return err
}

func (h *customHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *customHandler) WithGroup(name string) slog.Handler {
	return h
}
