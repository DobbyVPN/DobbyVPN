package log

import (
	stdlog "log"
	"log/slog"
	"strings"

	tunlog "github.com/xjasonlyu/tun2socks/v2/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	stdlog.SetFlags(0)
	stdlog.SetOutput(standardWriter{})
	tunlog.SetLogger(zap.New(zapBridge{}, zap.WithFatalHook(zapcore.WriteThenPanic)))
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
