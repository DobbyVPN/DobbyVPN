package telemetry

import (
	"context"
	"testing"
)

func TestSetupOTelSDKIsLocalOnlyCompatibilityNoOp(t *testing.T) {
	shutdown, err := SetupOTelSDK(context.Background(), "collector.invalid:4318", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
