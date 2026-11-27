package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"core/sessionapi"
)

func TestSnapshotCarriesTheNextRetryTime(t *testing.T) {
	at := time.UnixMilli(1795000000123)
	data, err := json.Marshal(SnapshotFrom(sessionapi.SnapshotResult{State: sessionapi.StateFailed, NextRetryAt: at}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"next_retry_at_unix_ms":1795000000123`) {
		t.Fatalf("snapshot = %s", data)
	}
	data, err = json.Marshal(SnapshotFrom(sessionapi.SnapshotResult{State: sessionapi.StateFailed}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "next_retry_at_unix_ms") {
		t.Fatalf("snapshot without a retry = %s", data)
	}
}
