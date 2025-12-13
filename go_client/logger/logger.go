package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

type Logger struct {
	file   *os.File
	logger *slog.Logger
}

var (
	lg     = &Logger{}
	initMu sync.Mutex
)

func maskStr(input string) string {
	runes := []rune(input)

	switch len(runes) {
	case 0:
		return ""
	case 1, 2:
		return input
	default:
		return string(runes[0]) + "***" + string(runes[len(runes)-1])
	}
}

func SetPath(path string) error {
	if lg.logger != nil {
		return nil
	}

	initMu.Lock()
	defer initMu.Unlock()

	if lg.logger != nil {
		return nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open log file: %w", err)
	}

	lg.file = f
	lg.logger = slog.New(&simpleHandler{file: f})
	return nil
}

func Infof(format string, args ...any) {
	if lg.logger == nil {
		return
	}
	lg.logger.Info(fmt.Sprintf(format, args...))
}

type simpleHandler struct {
	file *os.File
}

func (h *simpleHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *simpleHandler) Handle(_ context.Context, r slog.Record) error {
	t := time.Now().Format("2006-01-02 15:04:05")

	_, err := fmt.Fprintf(
		h.file,
		"[%s] \"%s\" [from go]\n",
		t,
		r.Message,
	)

	return err
}

func (h *simpleHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *simpleHandler) WithGroup(_ string) slog.Handler {
	return h
}
