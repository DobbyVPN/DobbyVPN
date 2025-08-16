package outline

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	client := NewClient("test_config")
	if client.driver == nil {
		t.Errorf("expected driver to be initialized")
	}
}
