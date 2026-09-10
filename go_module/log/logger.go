package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const logSchema = "dobby.log/v1"
const logSource = "go"

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
	file    *os.File
	logger  *slog.Logger
	pending []pendingEntry
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
		// Hooks do not replace Logrus's default output. Suppress that duplicate
		// path and route every entry into the same structured log.
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
// pre-initialization state.
func Close() error {
	initMu.Lock()
	defer initMu.Unlock()
	var err error
	if lg.file != nil {
		err = lg.file.Close()
	}
	lg.file = nil
	lg.logger = nil
	return err
}

// SetPath creates or reuses the local log file.
func SetPath(path string) error {
	initMu.Lock()
	defer initMu.Unlock()
	if lg.logger != nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open local log file: %w", err)
	}
	lg.file = f
	lg.logger = slog.New(newJSONLineHandler(f))
	lg.dumpBuffer()
	return nil
}

// SetOpenedFile installs an already-opened append-only log supplied by an
// external supervisor. The logger takes ownership of file.
func SetOpenedFile(file *os.File) error {
	if file == nil {
		return fmt.Errorf("managed log file is unavailable")
	}
	initMu.Lock()
	defer initMu.Unlock()
	if lg.logger != nil {
		return file.Close()
	}
	lg.file = file
	lg.logger = slog.New(newJSONLineHandler(file))
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
		slog.String("schema", logSchema),
		slog.String("source", logSource),
		slog.String("event", event),
		slog.String("category", category),
	)
	keys := make([]string, 0, len(arguments))
	for key := range arguments {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		attrs = append(attrs, slog.Any(key, arguments[key]))
	}
	record := slog.NewRecord(occurredAt, level, fmt.Sprintf("[%s] %s", category, message), 0)
	record.AddAttrs(attrs...)
	if err := logger.Handler().Handle(ctx, record); err != nil {
		fmt.Fprintf(os.Stderr, "write structured log: %v\n", err)
	}
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

func newJSONLineHandler(writer io.Writer) slog.Handler {
	return slog.NewJSONHandler(writer, &slog.HandlerOptions{
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
}
