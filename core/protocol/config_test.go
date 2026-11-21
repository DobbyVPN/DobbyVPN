package protocol

import (
	"reflect"
	"testing"
)

func TestCloneFieldsSeparatesNestedAttemptSettings(t *testing.T) {
	accepted := map[string]any{
		"outbounds": []map[string]any{{"settings": map[string]any{"servers": []any{"original"}}}},
		"endpoint":  map[string]any{"addresses": []string{"192.0.2.1:443"}},
	}
	snapshot := CloneFields(accepted)
	attempt := CloneFields(accepted)
	attempt["outbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)["servers"].([]any)[0] = "attempt"
	attempt["endpoint"].(map[string]any)["addresses"].([]any)[0] = "192.0.2.2:443"
	if !reflect.DeepEqual(snapshot, CloneFields(accepted)) {
		t.Fatal("attempt mutated accepted fields")
	}
}
