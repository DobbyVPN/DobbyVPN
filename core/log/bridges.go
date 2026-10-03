package log

import (
	"core/diagnostics"
	"fmt"
	stdlog "log"
	"log/slog"
	"strings"
	"time"

	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/grpclog"
	netlog "gvisor.dev/gvisor/pkg/log"
)

func init() {
	stdlog.SetFlags(0)
	stdlog.SetOutput(standardWriter{})
	netlog.SetTarget(netstackEmitter{})
	grpclog.SetLoggerV2(grpcBridge{})
	tunlog.SetLogger(zap.New(zapBridge{}, zap.WithFatalHook(zapcore.WriteThenPanic), zap.ErrorOutput(zapcore.AddSync(diagnostics.Stderr))))
}

type standardWriter struct{}

func (standardWriter) Write(data []byte) (int, error) {
	Info("STDLIB", strings.TrimSuffix(string(data), "\n"), nil)
	return len(data), nil
}

type zapBridge struct{ fields []zapcore.Field }

func (zapBridge) Enabled(zapcore.Level) bool { return true }
func (bridge zapBridge) With(fields []zapcore.Field) zapcore.Core {
	combined := make([]zapcore.Field, 0, len(bridge.fields)+len(fields))
	combined = append(combined, bridge.fields...)
	combined = append(combined, fields...)
	return zapBridge{combined}
}
func (bridge zapBridge) Check(entry zapcore.Entry, checked *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	return checked.AddCore(entry, bridge)
}
func (bridge zapBridge) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	attributes := zapcore.NewMapObjectEncoder()
	for _, field := range bridge.fields {
		field.AddTo(attributes)
	}
	for _, field := range fields {
		field.AddTo(attributes)
	}
	level := slog.Level(entry.Level) * 4
	if level > slog.LevelError {
		level = slog.LevelError
	}
	writeEventAt(entry.Time, level, "library.log", "TUN2SOCKS", entry.Message, attributes.Fields)
	return nil
}
func (zapBridge) Sync() error { return nil }

type netstackEmitter struct{}

func (netstackEmitter) Emit(_ int, level netlog.Level, timestamp time.Time, format string, args ...any) {
	severity := slog.LevelInfo
	if level == netlog.Warning {
		severity = slog.LevelWarn
	}
	if level == netlog.Debug {
		severity = slog.LevelDebug
	}
	writeEventAt(timestamp, severity, "library.log", "NETSTACK", fmt.Sprintf(format, args...), nil)
}

// gRPC's default logger captures os.Stderr at package initialization. Install
// the public adapter before transport creation, preserving producer severity.
type grpcBridge struct{}

func (grpcBridge) Info(args ...any) { Info("GRPC", fmt.Sprint(args...), nil) }
func (grpcBridge) Infoln(args ...any) {
	Info("GRPC", strings.TrimSuffix(fmt.Sprintln(args...), "\n"), nil)
}
func (grpcBridge) Infof(format string, args ...any) { Infof("GRPC", format, args...) }
func (grpcBridge) Warning(args ...any)              { Warn("GRPC", fmt.Sprint(args...), nil) }
func (grpcBridge) Warningln(args ...any) {
	Warn("GRPC", strings.TrimSuffix(fmt.Sprintln(args...), "\n"), nil)
}
func (grpcBridge) Warningf(format string, args ...any) { Warnf("GRPC", format, args...) }
func (grpcBridge) Error(args ...any)                   { Error("GRPC", fmt.Sprint(args...), nil) }
func (grpcBridge) Errorln(args ...any) {
	Error("GRPC", strings.TrimSuffix(fmt.Sprintln(args...), "\n"), nil)
}
func (grpcBridge) Errorf(format string, args ...any) { Errorf("GRPC", format, args...) }
func (grpcBridge) Fatal(args ...any)                 { grpcFatal(fmt.Sprint(args...)) }
func (grpcBridge) Fatalln(args ...any)               { grpcFatal(strings.TrimSuffix(fmt.Sprintln(args...), "\n")) }
func (grpcBridge) Fatalf(format string, args ...any) { grpcFatal(fmt.Sprintf(format, args...)) }
func (grpcBridge) V(level int) bool                  { return level <= 0 }
func grpcFatal(message string) {
	Error("GRPC", message, nil)
	panic(message)
}
