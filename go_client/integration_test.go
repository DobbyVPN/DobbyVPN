package main

import (
	"go_client/awg"
	"go_client/outline"
	"testing"
)

func TestOutlineClientIntegration(t *testing.T) {
	client := outline.NewClient("test_config")
	if client == nil {
		t.Fatalf("failed to create client")
	}

	err := client.Connect()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	err = client.Disconnect()
	if err != nil {
		t.Fatalf("failed to disconnect: %v", err)
	}
}

func TestAwgClientIntegration(t *testing.T) {
	config := awg.Config{
		Config: "test_config",
	}
	client, err := awg.NewClient(config)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if client == nil {
		t.Fatalf("failed to create client")
	}

	err = client.Connect()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	err = client.Disconnect()
	if err != nil {
		t.Fatalf("failed to disconnect: %v", err)
	}
}
