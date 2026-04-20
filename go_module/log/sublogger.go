package log

import (
	"bytes"
	"context"
	"fmt"
	"go_module/telemetry"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
)

type ISubLogger interface {
	Info(message string, arguments map[string]string)
	Debug(message string, arguments map[string]string)
	Error(message string, arguments map[string]string)
	Warn(message string, arguments map[string]string)
	NewSubLogger(category string) *SubLogger
}

type SubLogger struct {
	category string
	parent   ISubLogger
}

func (logger *SubLogger) Info(message string, arguments map[string]string) {
	msg := fmt.Sprintf("[%s] %s", logger.category, message)
	logger.parent.Info(msg, arguments)
}

func (logger *SubLogger) Debug(message string, arguments map[string]string) {
	msg := fmt.Sprintf("[%s] %s", logger.category, message)
	logger.parent.Debug(msg, arguments)
}

func (logger *SubLogger) Error(message string, arguments map[string]string) {
	msg := fmt.Sprintf("[%s] %s", logger.category, message)
	logger.parent.Error(msg, arguments)
}

func (logger *SubLogger) Warn(message string, arguments map[string]string) {
	msg := fmt.Sprintf("[%s] %s", logger.category, message)
	logger.parent.Warn(msg, arguments)
}

func (logger *SubLogger) NewSubLogger(category string) *SubLogger {
	return &SubLogger{
		category: category,
		parent:   logger,
	}
}

type RootLogger struct {
	stdoutLogger    *Logger
	telemetryLogger *TelemetryLogger
}

func (logger *RootLogger) prepareLog(message string, arguments map[string]string) string {
	var msg bytes.Buffer
	msg.WriteString(message)

	for key, value := range arguments {
		msg.WriteString(fmt.Sprintf(" %s=%s", key, value))
	}

	return msg.String()
}

func (logger *RootLogger) Info(message string, arguments map[string]string) {
	logger.stdoutLogger.Info(logger.prepareLog(message, arguments))
	logger.telemetryLogger.Info(message, arguments)
}

func (logger *RootLogger) Debug(message string, arguments map[string]string) {
	logger.stdoutLogger.Debug(logger.prepareLog(message, arguments))
	logger.telemetryLogger.Debug(message, arguments)
}

func (logger *RootLogger) Error(message string, arguments map[string]string) {
	logger.stdoutLogger.Error(logger.prepareLog(message, arguments))
	logger.telemetryLogger.Error(message, arguments)
}

func (logger *RootLogger) Warn(message string, arguments map[string]string) {
	logger.stdoutLogger.Warn(logger.prepareLog(message, arguments))
	logger.telemetryLogger.Warn(message, arguments)
}

func (logger *RootLogger) NewSubLogger(category string) *SubLogger {
	return &SubLogger{
		category: category,
		parent:   logger,
	}
}

type Logger struct {
	file   *os.File
	logger *slog.Logger
	buffer []string
}

func (logger *Logger) Info(message string) {
	if lg.logger == nil {
		logger.buffer = append(logger.buffer, message)
	} else {
		logger.logger.Info(message)
	}
}

func (logger *Logger) Debug(message string) {
	if lg.logger == nil {
		logger.buffer = append(logger.buffer, message)
	} else {
		logger.logger.Debug(message)
	}
}

func (logger *Logger) Error(message string) {
	if lg.logger == nil {
		logger.buffer = append(logger.buffer, message)
	} else {
		logger.logger.Error(message)
	}
}

func (logger *Logger) Warn(message string) {
	if lg.logger == nil {
		logger.buffer = append(logger.buffer, message)
	} else {
		logger.logger.Warn(message)
	}
}

func NewLogger() *Logger {
	return &Logger{buffer: []string{}}
}

const name = "https://github.com/DobbyVPN/DobbyVPN/go_module/log"

var (
	otelTracer = otel.Tracer(name)
	otelMeter  = otel.Meter(name)
	otelLogger = otelslog.NewLogger(name)
)

type TelemetryLogger struct {
	ctx      context.Context
	shutdown func(context.Context) error
}

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

func (logger *TelemetryLogger) flattenArgs(arguments map[string]string) []any {
	count := len(arguments)
	all := make([]any, count*2)
	i := 0
	for k, v := range arguments {
		all[i], all[i+1] = k, v
		i += 2
	}
	return all
}

func (logger *TelemetryLogger) Info(message string, arguments map[string]string) {
	otelLogger.InfoContext(logger.ctx, message, logger.flattenArgs(arguments)...)
}

func (logger *TelemetryLogger) Debug(message string, arguments map[string]string) {
	otelLogger.DebugContext(logger.ctx, message, logger.flattenArgs(arguments)...)
}

func (logger *TelemetryLogger) Error(message string, arguments map[string]string) {
	otelLogger.ErrorContext(logger.ctx, message, logger.flattenArgs(arguments)...)
}

func (logger *TelemetryLogger) Warn(message string, arguments map[string]string) {
	otelLogger.WarnContext(logger.ctx, message, logger.flattenArgs(arguments)...)
}

func (logger *TelemetryLogger) Shutdown() {
	logger.shutdown(logger.ctx)
}
