package log

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"

	"core/buildinfo"
	"core/diagnostics"
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
	case logrus.DebugLevel:
		level = slog.LevelDebug
	case logrus.TraceLevel:
		level = slog.LevelDebug - 4
	}
	arguments := make(map[string]any, len(entry.Data))
	for key, value := range entry.Data {
		arguments[key] = value
	}
	writeEventAt(entry.Time, level, "library.log", "LOGRUS", entry.Message, arguments)
	return nil
}

type Logger struct {
	file    *diagnostics.Writer
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
		logrus.SetOutput(io.Discard)
		logrus.SetLevel(logrus.TraceLevel)
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
	file, err := diagnostics.OpenWriter(path)
	if err != nil {
		return fmt.Errorf("open rotating log: %w", err)
	}
	lg.file = file
	lg.logger = slog.New(newJSONLineHandler(policyWriter{writer: file}))

	lg.dumpBuffer()
	return nil
}

var processOrder atomic.Uint64
var processRun = fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
var policyContext []byte // Protected by initMu, like all production writes.

type policyWriter struct{ writer *diagnostics.Writer }

func (writer policyWriter) Write(record []byte) (int, error) {
	_, err := writer.writer.WriteRecord(func(first bool) ([]byte, error) {
		if !first || len(policyContext) == 0 || bytes.Contains(record, []byte(`"policy_context":`)) {
			return record, nil
		}
		// slog emits one complete JSON object and newline. Attach retained policy
		// to this event, preserving its timestamp and process ordering.
		if !bytes.HasSuffix(record, []byte("}\n")) {
			return nil, fmt.Errorf("structured log record is not a JSON line")
		}
		output := make([]byte, 0, len(record)+len(policyContext)+20)
		output = append(output, record[:len(record)-2]...)
		output = append(output, `,"policy_context":`...)
		output = append(output, policyContext...)
		output = append(output, '}', '\n')
		return output, nil
	})
	if err != nil {
		return 0, err
	}
	return len(record), nil
}

// SetPolicy records full routing context once per accepted configuration and
// attaches it again to the first event of every new retained generation.
func SetPolicy(digest string, policy any) error {
	data, err := json.Marshal(map[string]any{"digest": digest, "policy": policy})
	if err != nil {
		return err
	}
	initMu.Lock()
	policyContext = data
	initMu.Unlock()
	Info("ROUTING_POLICY", "configuration routing policy accepted", map[string]any{"configuration_digest": digest, "policy_context": json.RawMessage(data)})
	return nil
}

// SetPolicyDetail retains resolved attempt inputs with the accepted policy.
// Identical resolution is referenced by configuration digest without repeating
// the large context. Rotation always emits the latest complete context again.
func SetPolicyDetail(key string, value any) error {
	detail, err := json.Marshal(value)
	if err != nil {
		return err
	}
	initMu.Lock()
	retained := make(map[string]json.RawMessage)
	if len(policyContext) != 0 {
		if decodeErr := json.Unmarshal(policyContext, &retained); decodeErr != nil {
			initMu.Unlock()
			return decodeErr
		}
	}
	if bytes.Equal(retained[key], detail) {
		initMu.Unlock()
		return nil
	}
	retained[key] = detail
	data, err := json.Marshal(retained)
	if err == nil {
		policyContext = data
	}
	initMu.Unlock()
	if err != nil {
		return err
	}
	Info("ROUTING_POLICY", "resolved routing policy updated", map[string]any{"policy_context": json.RawMessage(data)})
	return nil
}

// Correlation follows the single Go session owner; producers with a specific
// callback identity can override these fields in their event arguments.
type Correlation struct {
	SessionID           string
	Generation          uint64
	ConfigurationDigest string
}

var currentCorrelation atomic.Pointer[Correlation]

func SetCorrelation(value Correlation) { currentCorrelation.Store(&value) }

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
		if level >= slog.LevelError {
			emitAt(slog.New(newJSONLineHandler(io.Discard)), occurredAt, level, event, category, message, arguments)
		}
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
	attrs := make([]slog.Attr, 0, 8+len(arguments))
	attrs = append(attrs,
		slog.String("schema", logSchema),
		slog.String("source", logSource),
		slog.String("event", event),
		slog.String("category", category),
		slog.Int("process_id", os.Getpid()),
		slog.String("run_id", processRun),
		slog.Uint64("process_sequence", processOrder.Add(1)),
		slog.Any("build", buildinfo.Fields()),
	)
	if identity := currentCorrelation.Load(); identity != nil {
		for key, value := range map[string]any{"session_id": identity.SessionID, "generation": identity.Generation, "configuration_digest": identity.ConfigurationDigest} {
			if _, explicit := arguments[key]; !explicit {
				attrs = append(attrs, slog.Any(key, value))
			}
		}
	}
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
	if level >= slog.LevelError {
		if err := newJSONLineHandler(diagnostics.Stderr).Handle(ctx, record.Clone()); err != nil {
			_, _ = fmt.Fprintf(diagnostics.Stderr, "mirror structured error failed: %v\n", err)
		}
	}
	if err := logger.Handler().Handle(ctx, record); err != nil {
		_, _ = fmt.Fprintf(diagnostics.Stderr, "write structured log failed: %v; ", err)
		fallback := slog.NewTextHandler(diagnostics.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug - 4})
		if fallbackErr := fallback.Handle(ctx, record.Clone()); fallbackErr != nil {
			_, _ = fmt.Fprintf(diagnostics.Stderr, "write fallback log failed: %v; event=%q category=%q message=%q arguments=%v\n",
				fallbackErr, event, category, message, arguments)
		}
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
