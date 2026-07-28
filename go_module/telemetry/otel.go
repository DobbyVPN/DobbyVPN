// Package telemetry preserves the retired telemetry API without exporting data.
package telemetry

import "context"

// SetupOTelSDK is retained for source compatibility. Remote OTLP export was
// removed: production logging is local-only and this function never creates a
// network client, uses an endpoint, or retains an authorization token.
func SetupOTelSDK(_ context.Context, _, _ string) (func(context.Context) error, error) {
	return func(context.Context) error { return nil }, nil
}
