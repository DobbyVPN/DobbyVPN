package log

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

var (
	forbiddenMu    sync.RWMutex
	forbiddenWords = make([]string, 0)
	urlPattern     = regexp.MustCompile(`(?i)\b(?:https?|ss|vless|vmess|trojan)://[^\s"']+`)
	// Network locations are credentials-adjacent data in a VPN client: a local
	// SOCKS listener can contain generated credentials and a remote name/IP
	// identifies a user's selected service.  Keep lifecycle and timing facts,
	// but never persist either kind of location in local logs.
	// Mask address literals wherever a dependency embeds them. Host names are
	// masked only when they carry an endpoint-related key or an explicit port;
	// masking every dotted word would also destroy useful source file names.
	networkEndpointPattern = regexp.MustCompile(`(?i)(?:[^\s:@]+:[^\s@]+@)?(?:\[[0-9a-f:.]+\](?::\d{1,5})?|\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b|\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z]{2,63}|local):\d{1,5}\b)`)
	secretPattern          = regexp.MustCompile(`(?i)(["']?(?:token|api[_-]?key|password|secret|credential|authorization|auth|endpoint|url|config|server(?:ip|_ip)?|host|address|remote|proxy|gateway|dest(?:ination)?|resolved)["']?\s*[:=]\s*)(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|[^\s,}\]]+)`)
	tomlPattern            = regexp.MustCompile(`(?m)^\s*\[{1,2}[A-Za-z0-9_.-]+\]{1,2}\s*$`)
	jsonConfig             = regexp.MustCompile(`["'][A-Za-z0-9_.-]+["']\s*:`)
)

func AddForbiddenWord(word string) {
	if word == "" {
		return
	}
	forbiddenMu.Lock()
	defer forbiddenMu.Unlock()
	for _, existing := range forbiddenWords {
		if existing == word {
			return
		}
	}
	forbiddenWords = append(forbiddenWords, word)
}

func RemoveForbiddenWord(word string) {
	forbiddenMu.Lock()
	defer forbiddenMu.Unlock()
	for i, existing := range forbiddenWords {
		if existing == word {
			forbiddenWords = append(forbiddenWords[:i], forbiddenWords[i+1:]...)
			return
		}
	}
}

// MaskStr is for callers that need a short human-readable secret marker.
func MaskStr(input string) string {
	if input == "" {
		return ""
	}
	return "[REDACTED]"
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{"config", "url", "endpoint", "token", "password", "secret", "credential", "authorization", "auth", "api_key", "apikey", "server", "host", "address"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func redactText(message string) string {
	// A TOML/JSON configuration is never useful in a diagnostic line and may
	// carry multiple credentials beyond any individual key-value pattern.
	if tomlPattern.MatchString(message) || (strings.Contains(message, "{") && jsonConfig.MatchString(message)) {
		return "[REDACTED CONFIGURATION]"
	}
	message = urlPattern.ReplaceAllString(message, "[REDACTED URL]")
	message = secretPattern.ReplaceAllString(message, "${1}[REDACTED]")
	message = networkEndpointPattern.ReplaceAllString(message, "[REDACTED ENDPOINT]")
	return message
}

func redactValue(key string, value any) any {
	if isSensitiveKey(key) {
		return "[REDACTED]"
	}
	switch typed := value.(type) {
	case string:
		return redactText(typed)
	case []byte:
		return "[REDACTED BINARY]"
	case map[string]any:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = redactValue(nestedKey, nestedValue)
		}
		return out
	case logrus.Fields:
		out := make(map[string]any, len(typed))
		for nestedKey, nestedValue := range typed {
			out[nestedKey] = redactValue(nestedKey, nestedValue)
		}
		return out
	default:
		return value
	}
}

func maskMessage(message string) string {
	forbiddenMu.RLock()
	words := append([]string(nil), forbiddenWords...)
	forbiddenMu.RUnlock()
	for _, word := range words {
		if word != "" {
			message = strings.ReplaceAll(message, word, "[REDACTED]")
		}
	}
	return redactText(message)
}

type logrusToSlogHook struct{}

func (*logrusToSlogHook) Levels() []logrus.Level { return logrus.AllLevels }
func (*logrusToSlogHook) Fire(entry *logrus.Entry) error {
	message := entry.Message
	if len(entry.Data) > 0 {
		message = fmt.Sprintf("%s | %v", message, redactValue("metadata", entry.Data))
	}
	level := slog.LevelDebug
	switch entry.Level {
	case logrus.PanicLevel, logrus.FatalLevel, logrus.ErrorLevel:
		level = slog.LevelError
	case logrus.WarnLevel:
		level = slog.LevelWarn
	case logrus.InfoLevel:
		level = slog.LevelInfo
	case logrus.DebugLevel, logrus.TraceLevel:
		level = slog.LevelDebug
	}
	write(level, "LOGRUS", message, nil)
	return nil
}

// TelemetryLogger remains as an empty compatibility type. It cannot transport
// data and intentionally stores neither endpoint nor token.
type TelemetryLogger struct{}

type Logger struct {
	file     *os.File
	logger   *slog.Logger
	debugBuf []string
	infoBuf  []string
	warnBuf  []string
	errorBuf []string
	fallback bool
}

var (
	lg     = &Logger{debugBuf: []string{}, infoBuf: []string{}, warnBuf: []string{}, errorBuf: []string{}}
	initMu sync.Mutex
	bridge sync.Once
)

func init() {
	bridge.Do(func() {
		// Hooks do not replace Logrus's default output. Suppress that raw path
		// and route every entry through the redacting bridge above.
		logrus.SetOutput(io.Discard)
		logrus.AddHook(&logrusToSlogHook{})
	})
}

func NewTelemetryLogger(_, _ string) (*TelemetryLogger, error) { return &TelemetryLogger{}, nil }

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
	logger.debugBuf = nil
	logger.infoBuf = nil
	logger.warnBuf = nil
	logger.errorBuf = nil
}

func IsInitialized() bool {
	initMu.Lock()
	defer initMu.Unlock()
	return lg.logger != nil
}

// Close releases the local log file and returns the logger to its buffered
// pre-initialization state. It is safe to call repeatedly during orderly
// process shutdown and allows permission tests to remove their temporary log.
func Close() error {
	initMu.Lock()
	defer initMu.Unlock()
	var err error
	if lg.file != nil {
		err = lg.file.Close()
	}
	lg.file = nil
	lg.logger = nil
	lg.fallback = false
	return err
}

// SetPath creates local log storage with owner-only permissions, correcting
// permissions on pre-existing files/directories as well.
func SetPath(path string) error {
	initMu.Lock()
	defer initMu.Unlock()
	if lg.logger != nil && !lg.fallback {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("secure log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot open local log file; falling back to stderr logging")
		lg.logger = slog.New(&simpleHandler{file: os.Stderr})
		lg.fallback = true
		lg.dumpBuffer()
		return fmt.Errorf("open local log file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("secure log file: %w", err)
	}
	lg.file = f
	lg.logger = slog.New(&simpleHandler{file: f})
	lg.fallback = false
	lg.dumpBuffer()
	return nil
}

// InitTelemetry is intentionally local-only. Its parameters are discarded so
// legacy callers cannot cause a remote request or leave secrets in memory.
func InitTelemetry(_, _ string) error {
	Warnf("LOG", "Remote telemetry is disabled; logs remain on this device")
	return nil
}

func SetupTelemetryAttributes(_ string) {
	Debugf("LOG", "Telemetry attributes ignored because remote telemetry is disabled")
}

func StopTelemetry() { Debugf("LOG", "Remote telemetry is disabled; no exporter to stop") }

func prepareLog(message string, arguments map[string]any) string {
	var out bytes.Buffer
	out.WriteString(maskMessage(message))
	for key, value := range arguments {
		fmt.Fprintf(&out, " %q=%q", key, fmt.Sprint(redactValue(key, value)))
	}
	return maskMessage(out.String())
}

func write(level slog.Level, category, message string, arguments map[string]any) {
	entry := prepareLog(fmt.Sprintf("[%s] %s", category, message), arguments)
	initMu.Lock()
	defer initMu.Unlock()
	if lg.logger == nil {
		switch level {
		case slog.LevelDebug:
			lg.debugBuf = append(lg.debugBuf, entry)
		case slog.LevelInfo:
			lg.infoBuf = append(lg.infoBuf, entry)
		case slog.LevelWarn:
			lg.warnBuf = append(lg.warnBuf, entry)
		case slog.LevelError:
			lg.errorBuf = append(lg.errorBuf, entry)
		default:
			lg.errorBuf = append(lg.errorBuf, entry)
		}
		return
	}
	lg.logger.Log(context.Background(), level, entry)
}

func Info(category, message string, arguments map[string]any) {
	write(slog.LevelInfo, category, message, arguments)
}
func Debug(category, message string, arguments map[string]any) {
	write(slog.LevelDebug, category, message, arguments)
}
func Warn(category, message string, arguments map[string]any) {
	write(slog.LevelWarn, category, message, arguments)
}
func Error(category, message string, arguments map[string]any) {
	write(slog.LevelError, category, message, arguments)
}
func Infof(category, format string, args ...any)  { Info(category, fmt.Sprintf(format, args...), nil) }
func Debugf(category, format string, args ...any) { Debug(category, fmt.Sprintf(format, args...), nil) }
func Warnf(category, format string, args ...any)  { Warn(category, fmt.Sprintf(format, args...), nil) }
func Errorf(category, format string, args ...any) { Error(category, fmt.Sprintf(format, args...), nil) }

type simpleHandler struct{ file *os.File }

func (*simpleHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *simpleHandler) Handle(_ context.Context, record slog.Record) error {
	_, err := fmt.Fprintf(h.file, "[%s] [%s] %q [from go]\n", record.Time.Format("2006-01-02 15:04:05"), record.Level.String(), maskMessage(record.Message))
	return err
}
func (h *simpleHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *simpleHandler) WithGroup(string) slog.Handler      { return h }
