package log

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"go_module/auth"
	"go_module/telemetry"
	"io"
	"log/slog"
	"math/big"
	"net/http"
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

type TelemetryLogger struct {
	ctx              context.Context
	shutdown         func(context.Context) error
	endpoint         string
	externalIP       string
	connectionConfig map[string]any
}

type Logger struct {
	file     *os.File
	tlogger  *TelemetryLogger
	logger   *slog.Logger
	debugBuf []string
	infoBuf  []string
	warnBuf  []string
	errorBuf []string
}

var (
	lg     = &Logger{debugBuf: []string{}, infoBuf: []string{}, warnBuf: []string{}, errorBuf: []string{}}
	initMu sync.Mutex
)

const (
	YandexIPAPI string = "https://yandex.ru/internet/api/v0/ip"
)

var (
	clientID string = auth.GenerateRandomAuth()
)

// Set up OpenTelemetry.
func NewTelemetryLogger(endpoint, token string) (*TelemetryLogger, error) {
	ctx := context.Background()
	otelShutdown, err := telemetry.SetupOTelSDK(ctx, endpoint, token)
	if err != nil {
		return nil, fmt.Errorf("failed create otlp logger: %w", err)
	}

	return &TelemetryLogger{ctx: ctx, shutdown: otelShutdown, endpoint: endpoint}, nil
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

func IsInitialized() bool {
	initMu.Lock()
	defer initMu.Unlock()
	return lg.logger != nil
}

func SetPath(path string) error {
	initMu.Lock()
	defer initMu.Unlock()

	if lg.logger != nil {
		return nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644) //nolint:gosec // G302: logs should be readable
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot open log file %s: %v\n", path, err)
		fmt.Fprintf(os.Stderr, "Falling back to stderr logging\n")
		lg.dumpBuffer()
		logrus.AddHook(&logrusToSlogHook{})
		return fmt.Errorf("cannot open log file: %w", err)
	}

	lg.file = f
	lg.logger = slog.New(&simpleHandler{file: f})
	lg.dumpBuffer()

	logrus.AddHook(&logrusToSlogHook{})

	return nil
}

func InitTelemetry(endpoint, token string) error {
	if lg.tlogger != nil {
		Debugf("LOG", "No need to create new OpenTelemetry SDK")
		return nil
	}

	Debugf("LOG", "Create new OpenTelemetry SDK")
	tlg, err := NewTelemetryLogger(endpoint, token)
	if err != nil {
		Warnf("OTEL", "Failed to create new telemetry logger: %v", err)
		return fmt.Errorf("failed to create new telemetry logger: %w", err)
	}
	lg.tlogger = tlg

	return nil
}

func loadExternalIPStep() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", YandexIPAPI, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("failed create Yandex API request: %w", err)
	}

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed send Yandex API request: %w", err)
	}
	defer func() {
		bodyErr := resp.Body.Close()
		if bodyErr != nil {
			Warnf("LOG", "Failed close response body: %v", bodyErr)
		}
	}()

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed read Yandex API response: %w", err)
	}
	resultString := string(result)
	if len(resultString) < 2 {
		return "", fmt.Errorf("Yandex API returned an empty IP response")
	}

	return resultString[1 : len(resultString)-1], nil
}

func jitterDelay(minDelay, maxDelay time.Duration) time.Duration {
	if maxDelay <= minDelay {
		return minDelay
	}

	delta := maxDelay - minDelay
	randomOffset, err := rand.Int(rand.Reader, big.NewInt(int64(delta)))
	if err != nil {
		Warnf("LOG", "unable to generate rand number %v", err)
		return minDelay
	}

	return minDelay + time.Duration(randomOffset.Int64())
}

func loadExternalIP() string {
	minDelay := 200 * time.Millisecond
	maxDelay := 500 * time.Millisecond
	Debugf("LOG", "Loading user external IP")
	for i := 0; i < 5; i++ {
		if i > 0 {
			time.Sleep(jitterDelay(minDelay, maxDelay))
			Warnf("LOG", "Retry # %d", i)
		}

		extIP, err := loadExternalIPStep()
		if err != nil {
			Warnf("LOG", "Failed to load external IP: %v", err)
			continue
		}
		return extIP
	}

	return "nan"
}

func SetupTelemetryAttributes(config string) {
	Debugf("LOG", "Loading user external IP")

	externalIP := loadExternalIP()
	Debugf("LOG", "Loaded external IP")

	var connectionConfig map[string]any
	err := json.Unmarshal([]byte(config), &connectionConfig)
	if err != nil {
		Warnf("LOG", "Failed parse config json")
		connectionConfig = map[string]any{}
	}

	initMu.Lock()
	defer initMu.Unlock()
	if lg.tlogger != nil {
		lg.tlogger.externalIP = externalIP
		lg.tlogger.connectionConfig = connectionConfig
	}
}

func StopTelemetry() {
	if lg.tlogger != nil {
		if err := lg.tlogger.shutdown(lg.tlogger.ctx); err != nil {
			Warnf("LOG", "Telemetry shutdown error: %v", err)
		}
		lg.tlogger = nil
	} else {
		Warnf("LOG", "No telemetry logger found")
	}
}

const name = "https://github.com/DobbyVPN/DobbyVPN/go_module/log"

var (
	_          = otel.Tracer(name)
	_          = otel.Meter(name)
	otelLogger = otelslog.NewLogger(name)
)

// New logging
func prepareLog(message string, arguments map[string]any) string {
	var msg bytes.Buffer
	msg.WriteString(message)

	for key, value := range arguments {
		fmt.Fprintf(&msg, " %q=\"%v\"", key, value)
	}

	return msg.String()
}

func flattenArgs(argumentsList ...map[string]any) []any {
	size := 0
	for _, arguments := range argumentsList {
		size += len(arguments)
	}
	all := make([]any, 4+2*size)
	all[0] = "externalIP"
	all[1] = lg.tlogger.externalIP
	all[2] = "clientID"
	all[3] = clientID
	i := 4
	for _, arguments := range argumentsList {
		for k, v := range arguments {
			all[i], all[i+1] = k, v
			i += 2
		}
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

func Info(category, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_info(categoryMessage, arguments)
	if lg.tlogger != nil {
		otelLogger.InfoContext(lg.tlogger.ctx, categoryMessage, flattenArgs(arguments, lg.tlogger.connectionConfig)...)
	}
}

func Debug(category, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_debug(categoryMessage, arguments)
	if lg.tlogger != nil {
		otelLogger.DebugContext(lg.tlogger.ctx, categoryMessage, flattenArgs(arguments, lg.tlogger.connectionConfig)...)
	}
}

func Warn(category, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_warn(categoryMessage, arguments)
	if lg.tlogger != nil {
		otelLogger.WarnContext(lg.tlogger.ctx, categoryMessage, flattenArgs(arguments, lg.tlogger.connectionConfig)...)
	}
}

func Error(category, message string, arguments map[string]any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, message)
	_error(categoryMessage, arguments)
	if lg.tlogger != nil {
		otelLogger.ErrorContext(lg.tlogger.ctx, categoryMessage, flattenArgs(arguments, lg.tlogger.connectionConfig)...)
	}
}

func Infof(category, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_info(categoryMessage, make(map[string]any))
	if lg.tlogger != nil {
		otelLogger.InfoContext(lg.tlogger.ctx, categoryMessage, flattenArgs(lg.tlogger.connectionConfig)...)
	}
}

func Debugf(category, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_debug(categoryMessage, make(map[string]any))
	if lg.tlogger != nil {
		otelLogger.DebugContext(lg.tlogger.ctx, categoryMessage, flattenArgs(lg.tlogger.connectionConfig)...)
	}
}

func Warnf(category, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_warn(categoryMessage, make(map[string]any))
	if lg.tlogger != nil {
		otelLogger.WarnContext(lg.tlogger.ctx, categoryMessage, flattenArgs(lg.tlogger.connectionConfig)...)
	}
}

func Errorf(category, format string, args ...any) {
	categoryMessage := fmt.Sprintf("[%s] %s", category, fmt.Sprintf(format, args...))
	_error(categoryMessage, make(map[string]any))
	if lg.tlogger != nil {
		otelLogger.ErrorContext(lg.tlogger.ctx, categoryMessage, flattenArgs(lg.tlogger.connectionConfig)...)
	}
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
		"[%s] [%s] %q [from go]\n",
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
