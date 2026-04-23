package log

import (
	"bytes"
	"context"
	"fmt"
	"go_module/telemetry"
	"log/slog"
	"os"
	"strings"
	"sync"

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
	file     *os.File
	logger   *slog.Logger
	debugBuf []string
	infoBuf  []string
	warnBuf  []string
	errorBuf []string
}

type TelemetryLogger struct {
	ctx      context.Context
	shutdown func(context.Context) error
}

var (
	lg     = &Logger{debugBuf: []string{}, infoBuf: []string{}, warnBuf: []string{}, errorBuf: []string{}}
	tlg    = NewTelemetryLogger()
	initMu sync.Mutex
)

func NewTelemetryLogger() *TelemetryLogger {
	// Set up OpenTelemetry.
	ctx := context.Background()
	otelShutdown, err := telemetry.SetupOTelSDK(ctx)
	if err != nil {
		return &TelemetryLogger{ctx: ctx}
	} else {
		return &TelemetryLogger{ctx: ctx, shutdown: otelShutdown}
	}
}

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
	for _, message := range logger.debugBuf {
		logger.logger.Debug(message)
	}
	for _, message := range logger.infoBuf {
		logger.logger.Info(message)
	}
	for _, message := range logger.warnBuf {
		logger.logger.Warn(message)
	}
	for _, message := range logger.errorBuf {
		logger.logger.Error(message)
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
		lg.debugBuf = append(lg.debugBuf, message)
	} else {
		lg.logger.Debug(message)
	}
	otelLogger.DebugContext(tlg.ctx, message)
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

func _debug(categoryMessage string, arguments map[string]any) {
	stdoutMessage := prepareLog(categoryMessage, arguments)
	if lg.logger == nil {
		lg.debugBuf = append(lg.debugBuf, stdoutMessage)
	} else {
		lg.logger.Debug(stdoutMessage)
	}
}

func _info(categoryMessage string, arguments map[string]any) {
	stdoutMessage := prepareLog(categoryMessage, arguments)
	if lg.logger == nil {
		lg.infoBuf = append(lg.infoBuf, stdoutMessage)
	} else {
		lg.logger.Info(stdoutMessage)
	}
}

func _warn(categoryMessage string, arguments map[string]any) {
	stdoutMessage := prepareLog(categoryMessage, arguments)
	if lg.logger == nil {
		lg.warnBuf = append(lg.warnBuf, stdoutMessage)
	} else {
		lg.logger.Warn(stdoutMessage)
	}
}

func _error(categoryMessage string, arguments map[string]any) {
	stdoutMessage := prepareLog(categoryMessage, arguments)
	if lg.logger == nil {
		lg.errorBuf = append(lg.errorBuf, stdoutMessage)
	} else {
		lg.logger.Error(stdoutMessage)
	}
}

func Info(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_info(categoryMessage, arguments)
	otelLogger.InfoContext(tlg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func Debug(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_debug(categoryMessage, arguments)
	otelLogger.DebugContext(tlg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func Warn(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_warn(categoryMessage, arguments)
	otelLogger.WarnContext(tlg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func Error(category string, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_error(categoryMessage, arguments)
	otelLogger.ErrorContext(tlg.ctx, categoryMessage, flattenArgs(arguments)...)
}

func SimpleInfo(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_info(categoryMessage, make(map[string]any))
	otelLogger.InfoContext(tlg.ctx, categoryMessage)
}

func SimpleDebug(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_debug(categoryMessage, make(map[string]any))
	otelLogger.DebugContext(tlg.ctx, categoryMessage)
}

func SimpleWarn(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_warn(categoryMessage, make(map[string]any))
	otelLogger.WarnContext(tlg.ctx, categoryMessage)
}

func SimpleError(category string, message string) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_error(categoryMessage, make(map[string]any))
	otelLogger.ErrorContext(tlg.ctx, categoryMessage)
}

func SimpleInfof(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_info(categoryMessage, make(map[string]any))
	otelLogger.InfoContext(tlg.ctx, categoryMessage)
}

func SimpleDebugf(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_debug(categoryMessage, make(map[string]any))
	otelLogger.DebugContext(tlg.ctx, categoryMessage)
}

func SimpleWarnf(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_warn(categoryMessage, make(map[string]any))
	otelLogger.WarnContext(tlg.ctx, categoryMessage)
}

func SimpleErrorf(category string, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_error(categoryMessage, make(map[string]any))
	otelLogger.ErrorContext(tlg.ctx, categoryMessage)
}

type simpleHandler struct {
	file *os.File
}

func (h *simpleHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h *simpleHandler) Handle(_ context.Context, r slog.Record) error {
	_, err := fmt.Fprintf(
		h.file,
		"[%s] [%s] \"%s\" [from go]\n",
		r.Time.Format("2006-01-02 15:04:05"),
		r.Level.String(),
		maskMessage(r.Message),
	)

	return err
}

func (h *simpleHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h *simpleHandler) WithGroup(_ string) slog.Handler {
	return h
}
