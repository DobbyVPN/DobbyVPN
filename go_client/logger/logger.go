package logger

import (
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	forbiddenMu    sync.RWMutex
	forbiddenWords = make([]string, 0)
)

func AddForbiddenWord(word string) {
	if word == "" {
		return
	}
	forbiddenMu.Lock()
	defer forbiddenMu.Unlock()
	for _, w := range forbiddenWords {
		if w == word {
			return
		}
	}
	forbiddenWords = append(forbiddenWords, word)
}

func RemoveForbiddenWord(word string) {
	forbiddenMu.Lock()
	defer forbiddenMu.Unlock()

	for i, w := range forbiddenWords {
		if w == word {
			forbiddenWords = append(forbiddenWords[:i], forbiddenWords[i+1:]...)
			return
		}
	}
}

func maskMessage(msg string) string {
	forbiddenMu.RLock()
	defer forbiddenMu.RUnlock()

	for _, w := range forbiddenWords {
		if w == "" {
			continue
		}

		for {
			idx := strings.Index(msg, w)
			if idx == -1 {
				break
			}

			masked := MaskStr(w)
			msg = msg[:idx] + masked + msg[idx+len(w):]
		}
	}

	return msg
}

type logrusToSlogHook struct{}

func (h *logrusToSlogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *logrusToSlogHook) Fire(e *logrus.Entry) error {
	if lg.logger == nil {
		return nil
	}

	msg := e.Message
	if len(e.Data) > 0 {
		msg = fmt.Sprintf("%s | %v", msg, e.Data)
	}

	switch e.Level {
	case logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel:
		lg.logger.Error(msg)
	case logrus.WarnLevel:
		lg.logger.Warn(msg)
	case logrus.InfoLevel:
		lg.logger.Info(msg)
	default:
		lg.logger.Debug(msg)
	}

	return nil
}

type Logger struct {
	file   *os.File
	logger *slog.Logger
}

var (
	lg     = &Logger{}
	initMu sync.Mutex
)

func MaskStr(input string) string {
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

	logrus.AddHook(&logrusToSlogHook{})

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

	msg := maskMessage(r.Message)

	_, err := fmt.Fprintf(
		h.file,
		"[%s] \"%s\" [from go]\n",
		t,
		msg,
	)

	return err
}

func (h *simpleHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *simpleHandler) WithGroup(_ string) slog.Handler {
	return h
}
