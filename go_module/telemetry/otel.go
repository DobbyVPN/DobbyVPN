package telemetry

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/log"
)

const (
	ExporterBatchSizeMin int = 1024
	ExporterBatchSizeMax int = 4096
	ExporterBufferSize   int = 16384

	ExporterIntervalMin     time.Duration = 1 * time.Second
	ExporterIntervalMax     time.Duration = 3 * time.Second
	ExporterTimeoutMin      time.Duration = 5 * time.Second
	ExporterTimeoutMax      time.Duration = 10 * time.Second
	RetryInitialIntervalMin time.Duration = 500 * time.Millisecond
	RetryInitialIntervalMax time.Duration = 2 * time.Second
	RetryMaxIntervalMin     time.Duration = 10 * time.Second
	RetryMaxIntervalMax     time.Duration = 20 * time.Second
	RetryMaxElapsedTimeMin  time.Duration = 5 * time.Minute
	RetryMaxElapsedTimeMax  time.Duration = 10 * time.Minute
)

// setupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func SetupOTelSDK(ctx context.Context, endpoint, token string) (func(context.Context) error, error) {
	var shutdownFuncs []func(context.Context) error
	var err error

	// shutdown calls cleanup functions registered via shutdownFuncs.
	// The errors from the calls are joined.
	// Each registered cleanup will be invoked once.
	shutdown := func(ctx context.Context) error {
		var err error
		for _, fn := range shutdownFuncs {
			err = errors.Join(err, fn(ctx))
		}
		shutdownFuncs = nil
		return err
	}

	// handleErr calls shutdown for cleanup and makes sure that all errors are returned.
	handleErr := func(inErr error) {
		err = errors.Join(inErr, shutdown(ctx))
	}

	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// Set up logger provider.
	loggerProvider, err := newLoggerProvider(ctx, endpoint, token)
	if err != nil {
		handleErr(err)
		return shutdown, err
	}
	shutdownFuncs = append(shutdownFuncs, loggerProvider.ForceFlush)
	shutdownFuncs = append(shutdownFuncs, loggerProvider.Shutdown)
	global.SetLoggerProvider(loggerProvider)

	return shutdown, err
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func randRange(min, max time.Duration) time.Duration {
	if min == 0 && max == 0 {
		return 0
	}
	return min + (max-min)*time.Duration(rand.Float32())
}

func newLoggerProvider(ctx context.Context, endpoint, token string) (*log.LoggerProvider, error) {
	logExporter, err := otlploghttp.New(
		ctx,
		otlploghttp.WithEndpoint(endpoint),
		otlploghttp.WithHeaders(map[string]string{
			"Authorization": token,
		}),
		otlploghttp.WithInsecure(),
		otlploghttp.WithRetry(
			otlploghttp.RetryConfig{
				Enabled:         true,
				InitialInterval: randRange(RetryInitialIntervalMin, RetryInitialIntervalMax),
				MaxInterval:     randRange(RetryMaxIntervalMin, RetryMaxIntervalMax),
				MaxElapsedTime:  randRange(RetryMaxElapsedTimeMin, RetryMaxElapsedTimeMax),
			},
		),
	)
	if err != nil {
		return nil, err
	}

	logProcessor := log.NewBatchProcessor(
		logExporter,
		log.WithExportBufferSize(ExporterBufferSize),
		log.WithExportMaxBatchSize(rand.Intn(ExporterBatchSizeMax-ExporterBatchSizeMin)+ExporterBatchSizeMin),
		log.WithExportInterval(randRange(ExporterIntervalMin, ExporterIntervalMax)),
		log.WithExportTimeout(randRange(ExporterTimeoutMin, ExporterTimeoutMax)),
	)
	loggerProvider := log.NewLoggerProvider(
		log.WithProcessor(logProcessor),
	)
	return loggerProvider, nil
}
