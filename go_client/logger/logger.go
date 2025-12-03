package logger

import (
	"fmt"
	"log/slog"
	"os"
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

	handler := slog.NewTextHandler(file, &slog.HandlerOptions{
		AddSource: true,
	})

	logger.file = file
	logger.path = path
	logger.logger = slog.New(handler)

	return nil
}

func Infof(format string, args ...any) {
	if logger.logger == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	logger.logger.Info(msg)
}
