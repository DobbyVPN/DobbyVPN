package log

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
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
	case logrus.DebugLevel, logrus.TraceLevel:
		lg.logger.Debug(msg)
	}

	return nil
}

type Logger struct {
	ctx    context.Context
	file   *os.File
	logger *slog.Logger
	buffer []string
}

var (
	lg     = &Logger{buffer: []string{}}
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

func (logger *Logger) dumpBuffer() {
	for _, message := range logger.buffer {
		logger.logger.Info(message)
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

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec // G302: logs should be readable
	if err != nil {
		return fmt.Errorf("cannot open log file: %w", err)
	}

	lg.ctx = context.Background()
	lg.file = f
	lg.logger = slog.New(&simpleHandler{file: f})
	lg.dumpBuffer()

	logrus.AddHook(&logrusToSlogHook{})

	return nil
}

const name = "https://github.com/DobbyVPN/DobbyVPN/go_module/log"

var (
	otelTracer = otel.Tracer(name)
	otelMeter  = otel.Meter(name)
	otelLogger = otelslog.NewLogger(name)
)

// Legacy:
func Infof(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if lg.logger == nil {
		lg.buffer = append(lg.buffer, message)
	} else {
		lg.logger.Info(message)
	}
	otelLogger.InfoContext(lg.ctx, message)
}

// New logging
func prepareLog(message string, arguments map[string]any) string {
	var msg bytes.Buffer
	msg.WriteString(message)

	for key, value := range arguments {
		msg.WriteString(fmt.Sprintf(" \"%s\"=\"%v\"", key, value))
	}

	return msg.String()
}

func flattenArgs(arguments map[string]any) []any {
	count := len(arguments)
	all := make([]any, count*2)
	i := 0
	for k, v := range arguments {
		all[i], all[i+1] = k, v
		i += 2
	}
	return all
}

func log(level, categoryMessage string, arguments map[string]any) {
	levelMessage := fmt.Sprintf("[%s] %s", level, categoryMessage)
	stdoutMessage := prepareLog(levelMessage, arguments)
	if lg.logger == nil {
		lg.buffer = append(lg.buffer, stdoutMessage)
	} else {
		lg.logger.Info(stdoutMessage)
	}
}

func Info(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("INFO", categoryMessage, arguments)
	otelLogger.InfoContext(lg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func Debug(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("DEBUG", categoryMessage, arguments)
	otelLogger.DebugContext(lg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func Warn(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("WARN", categoryMessage, arguments)
	otelLogger.WarnContext(lg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func Error(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("ERROR", categoryMessage, arguments)
	otelLogger.ErrorContext(lg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func SimpleInfo(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("INFO", categoryMessage, make(map[string]any))
	otelLogger.InfoContext(lg.ctx, categoryMessage)
}

func SimpleDebug(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("DEBUG", categoryMessage, make(map[string]any))
	otelLogger.DebugContext(lg.ctx, categoryMessage)
}

func SimpleWarn(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("WARN", categoryMessage, make(map[string]any))
	otelLogger.WarnContext(lg.ctx, categoryMessage)
}

func SimpleError(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	log("ERROR", categoryMessage, make(map[string]any))
	otelLogger.ErrorContext(lg.ctx, categoryMessage)
}

func SimpleInfof(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	levelMessage := fmt.Sprintf("[INFO] %s", categoryMessage)
	if lg.logger == nil {
		lg.buffer = append(lg.buffer, levelMessage)
	} else {
		lg.logger.Info(levelMessage)
	}
	otelLogger.InfoContext(lg.ctx, categoryMessage)
}

func SimpleDebugf(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	levelMessage := fmt.Sprintf("[DEBUG] %s", categoryMessage)
	if lg.logger == nil {
		lg.buffer = append(lg.buffer, levelMessage)
	} else {
		lg.logger.Debug(levelMessage)
	}
	otelLogger.DebugContext(lg.ctx, categoryMessage)
}

func SimpleWarnf(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	levelMessage := fmt.Sprintf("[WARN] %s", categoryMessage)
	if lg.logger == nil {
		lg.buffer = append(lg.buffer, levelMessage)
	} else {
		lg.logger.Warn(levelMessage)
	}
	otelLogger.WarnContext(lg.ctx, categoryMessage)
}

func SimpleErrorf(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	levelMessage := fmt.Sprintf("[ERROR] %s", categoryMessage)
	if lg.logger == nil {
		lg.buffer = append(lg.buffer, levelMessage)
	} else {
		lg.logger.Error(levelMessage)
	}
	otelLogger.ErrorContext(lg.ctx, categoryMessage)
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
