package awg

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	config := Config{
		Config: "test_config",
	}
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if client.driver == nil {
		t.Errorf("expected driver to be initialized")
	}
}
