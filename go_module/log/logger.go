package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	redactedValue    = "[REDACTED]"
	redactedEndpoint = "[REDACTED ENDPOINT]"
	logSchema        = "dobby.log/v1"
	logSource        = "go"
)

var sourceFileSuffixes = []string{
	".go", ".swift", ".kt", ".kts", ".proto", ".json", ".toml", ".yaml", ".yml", ".xml", ".md",
}

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
	ipv6Pattern            = regexp.MustCompile(`(?i)\b[0-9a-f]{1,4}(?::[0-9a-f]{0,4}){2,7}\b`)
	hostnamePattern        = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+(?:[a-z]{2,63}|local|invalid)\b`)
	stableEventPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	secretPattern          = regexp.MustCompile(`(?i)(["']?(?:token|api[_-]?key|password|secret|credential|authorization|auth|endpoint|url|config|server(?:ip|_ip)?|host|address|remote|proxy|gateway|dest(?:ination)?|resolved|path|file|directory|session[_-]?id|command[_-]?id)["']?\s*[:=]\s*)(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|[^\s,}\]]+)`)
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
	return redactedValue
}

func isSensitiveKey(key string) bool {
	key = strings.ToLower(key)
	for _, marker := range []string{
		"token", "api_key", "api-key", "apikey", "password", "secret", "credential", "authorization", "auth", "endpoint", "url", "config", "server", "host", "address",
		"remote", "proxy", "gateway", "dest", "resolved", "path", "file", "directory", "session-id", "session_id", "command-id", "command_id",
	} {
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
	message = secretPattern.ReplaceAllString(message, "${1}"+redactedValue)
	message = networkEndpointPattern.ReplaceAllString(message, redactedEndpoint)
	message = ipv6Pattern.ReplaceAllStringFunc(message, func(candidate string) string {
		if strings.Contains(candidate, "::") || strings.ContainsAny(strings.ToLower(candidate), "abcdef") || strings.Count(candidate, ":") >= 3 {
			return redactedEndpoint
		}
		return candidate
	})
	message = hostnamePattern.ReplaceAllStringFunc(message, func(candidate string) string {
		lower := strings.ToLower(candidate)
		for _, sourceSuffix := range sourceFileSuffixes {
			if strings.HasSuffix(lower, sourceSuffix) {
				return candidate
			}
		}
		return redactedEndpoint
	})
	return message
}

type trustedVocabulary string

func trustedEventName(event string) trustedVocabulary {
	if stableEventPattern.MatchString(event) {
		return trustedVocabulary(event)
	}
	return trustedVocabulary("log.message")
}

func redactValue(key string, value any) any {
	if isSensitiveKey(key) {
		return redactedValue
	}
	switch typed := value.(type) {
	case trustedVocabulary:
		return string(typed)
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
	case map[string]string:
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
	case []any:
		out := make([]any, len(typed))
		for index, nestedValue := range typed {
			out[index] = redactValue(key, nestedValue)
		}
		return out
	case error:
		return maskMessage(typed.Error())
	case fmt.Stringer:
		return maskMessage(typed.String())
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
			message = strings.ReplaceAll(message, word, redactedValue)
		}
	}
	return redactText(message)
}

type logrusToSlogHook struct{}

func (*logrusToSlogHook) Levels() []logrus.Level { return logrus.AllLevels }
func (*logrusToSlogHook) Fire(entry *logrus.Entry) error {
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
	arguments := make(map[string]any, len(entry.Data))
	for key, value := range entry.Data {
		arguments[key] = value
	}
	write(level, "LOGRUS", entry.Message, arguments)
	return nil
}

type Logger struct {
	file     *os.File
	logger   *slog.Logger
	pending  []pendingEntry
	fallback bool
}

type pendingEntry struct {
	occurredAt time.Time
	level      slog.Level
	event      string
	category   string
	message    string
	arguments  map[string]any
}

var (
	lg     = &Logger{}
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

func (logger *Logger) dumpBuffer() {
	for _, entry := range logger.pending {
		emitAt(logger.logger, entry.occurredAt, entry.level, entry.event, entry.category, entry.message, entry.arguments)
	}
	logger.pending = nil
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
		lg.logger = slog.New(newJSONLineHandler(os.Stderr))
		lg.fallback = true
		lg.dumpBuffer()
		return fmt.Errorf("open local log file: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("secure log file: %w", err)
	}
	lg.file = f
	lg.logger = slog.New(newJSONLineHandler(f))
	lg.fallback = false
	lg.dumpBuffer()
	return nil
}

func write(level slog.Level, category, message string, arguments map[string]any) {
	writeEvent(level, "log.message", category, message, arguments)
}

func writeEvent(level slog.Level, event, category, message string, arguments map[string]any) {
	writeEventAt(time.Now(), level, event, category, message, arguments)
}

func writeEventAt(occurredAt time.Time, level slog.Level, event, category, message string, arguments map[string]any) {
	if occurredAt.IsZero() {
		occurredAt = time.Now()
	}
	initMu.Lock()
	defer initMu.Unlock()
	if lg.logger == nil {
		lg.pending = append(lg.pending, pendingEntry{
			occurredAt: occurredAt, level: level, event: event, category: category,
			message: message, arguments: cloneArguments(arguments),
		})
		return
	}
	emitAt(lg.logger, occurredAt, level, event, category, message, arguments)
}

func cloneArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return nil
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = value
	}
	return cloned
}

func emit(logger *slog.Logger, level slog.Level, event, category, message string, arguments map[string]any) {
	emitAt(logger, time.Now(), level, event, category, message, arguments)
}

func emitAt(logger *slog.Logger, occurredAt time.Time, level slog.Level, event, category, message string, arguments map[string]any) {
	ctx := context.Background()
	if !logger.Enabled(ctx, level) {
		return
	}
	attrs := make([]slog.Attr, 0, 4+len(arguments))
	attrs = append(attrs,
		slog.Any("schema", trustedVocabulary(logSchema)),
		slog.Any("source", trustedVocabulary(logSource)),
		slog.Any("event", trustedEventName(event)),
		slog.String("category", category),
	)
	keys := make([]string, 0, len(arguments))
	for key := range arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		attrs = append(attrs, slog.Any(key, redactValue(key, arguments[key])))
	}
	record := slog.NewRecord(occurredAt, level, maskMessage(fmt.Sprintf("[%s] %s", category, message)), 0)
	record.AddAttrs(attrs...)
	_ = logger.Handler().Handle(ctx, record)
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
func Trace(category, message string, arguments map[string]any) {
	write(slog.LevelDebug-4, category, message, arguments)
}
func Infof(category, format string, args ...any)  { Info(category, fmt.Sprintf(format, args...), nil) }
func Debugf(category, format string, args ...any) { Debug(category, fmt.Sprintf(format, args...), nil) }
func Warnf(category, format string, args ...any)  { Warn(category, fmt.Sprintf(format, args...), nil) }
func Errorf(category, format string, args ...any) { Error(category, fmt.Sprintf(format, args...), nil) }
func Tracef(category, format string, args ...any) { Trace(category, fmt.Sprintf(format, args...), nil) }

type redactingHandler struct{ next slog.Handler }

func newJSONLineHandler(writer io.Writer) slog.Handler {
	next := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: slog.LevelDebug - 4,
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			switch attribute.Key {
			case slog.TimeKey:
				attribute.Key = "timestamp"
				if timestamp, ok := attribute.Value.Any().(time.Time); ok {
					attribute.Value = slog.StringValue(timestamp.UTC().Format(time.RFC3339Nano))
				}
			case slog.MessageKey:
				attribute.Key = "message"
			case slog.LevelKey:
				if attribute.Value.String() == "DEBUG-4" {
					attribute.Value = slog.StringValue("TRACE")
				}
			}
			return attribute
		},
	})
	return &redactingHandler{next: next}
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	sanitized := slog.NewRecord(record.Time, record.Level, maskMessage(record.Message), record.PC)
	record.Attrs(func(attribute slog.Attr) bool {
		sanitized.AddAttrs(redactAttribute(attribute))
		return true
	})
	return h.next.Handle(ctx, sanitized)
}

func (h *redactingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attributes))
	for index, attribute := range attributes {
		redacted[index] = redactAttribute(attribute)
	}
	return &redactingHandler{next: h.next.WithAttrs(redacted)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name)}
}

func redactAttribute(attribute slog.Attr) slog.Attr {
	attribute.Value = attribute.Value.Resolve()
	if attribute.Value.Kind() == slog.KindGroup {
		group := attribute.Value.Group()
		redacted := make([]slog.Attr, len(group))
		for index, nested := range group {
			redacted[index] = redactAttribute(nested)
		}
		return slog.Group(attribute.Key, attrsToAny(redacted)...)
	}
	return slog.Any(attribute.Key, redactValue(attribute.Key, attribute.Value.Any()))
}

func attrsToAny(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index := range attributes {
		values[index] = attributes[index]
	}
	return values
}
