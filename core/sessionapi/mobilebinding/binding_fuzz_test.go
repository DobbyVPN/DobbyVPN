package mobilebinding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"core/sessionapi"
	"core/sessionapi/wire"
)

// fakeManager is the pure in-package seam for the shared command dispatcher:
// every method returns its zero result without touching a session, a socket or
// any external service.
type fakeManager struct{}

func (fakeManager) Configure(context.Context, string, uint64, []byte) (sessionapi.ConfigureResult, error) {
	return sessionapi.ConfigureResult{}, nil
}

func (fakeManager) Start(context.Context, string, uint64, sessionapi.StartTarget) (sessionapi.StartResult, error) {
	return sessionapi.StartResult{}, nil
}

func (fakeManager) Stop(context.Context, string, uint64) (sessionapi.StopResult, error) {
	return sessionapi.StopResult{}, nil
}

func (fakeManager) Snapshot(context.Context, string) (sessionapi.SnapshotResult, error) {
	return sessionapi.SnapshotResult{}, nil
}

func (fakeManager) PrepareLogUpload(context.Context, string, string) (sessionapi.LogUploadPreparation, error) {
	return sessionapi.LogUploadPreparation{}, nil
}

func (fakeManager) NetworkEvent(context.Context, string, string) error { return nil }

func decodeResponse(t *testing.T, response string) wire.Response[json.RawMessage] {
	t.Helper()
	var decoded wire.Response[json.RawMessage]
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatalf("response is not decodable: %v", err)
	}
	return decoded
}

// FuzzCallJSONParameters checks the shared desktop/iOS command-parameter
// decoder (CallJSONWithConfiguration) against untrusted method names and
// parameter bytes. Every response must be a decodable envelope carrying
// exactly one outcome; a panic anywhere fails the fuzz run.
func FuzzCallJSONParameters(f *testing.F) {
	binding := &Binding{manager: fakeManager{}}
	for _, seed := range []struct {
		method string
		params string
	}{
		{"Snapshot", `{"session_id":"s"}`},
		{"Snapshot", ``},
		{"Snapshot", `null`},
		{"Start", `{"mode":"` + string(sessionapi.AutoSelect) + `","index":-1,"expected_sequence":0}`},
		{"Start", `{"source":"[[Xray]]\noutbounds = []\n","index":0}`},
		{"Stop", `{"generation":0}`},
		{"Configure", `{"expected_sequence":-1}`},
		{"ParseImportLink", `{"link":"trojan://synthetic@host.invalid:443"}`},
		{"NetworkEvent", `{"reason":"network-change"}`},
		{"PrepareLogUpload", `{"session_id":"s","digest":"d"}`},
		{"Unknown", `{}`},
		{"", `{}`},
		{"Snapshot", `{"extra":1}`},
		{"Snapshot", `{"index":"x"}`},
		{"Snapshot", `not json`},
		{"Snapshot", `{"session_id":`},
	} {
		f.Add(seed.method, []byte(seed.params))
	}

	f.Fuzz(func(t *testing.T, method string, params []byte) {
		response := binding.CallJSON(context.Background(), method, params)
		decoded := decodeResponse(t, response)
		if decoded.OK {
			if decoded.Error != nil {
				t.Fatalf("successful response carries an error: %+v", decoded.Error)
			}
			return
		}
		if decoded.Error == nil {
			t.Fatalf("failed response carries no error: %s", response)
		}
		if decoded.Error.Code == "" {
			t.Fatalf("failed response carries no error code: %s", response)
		}
	})
}

// TestCallJSONParameterDecoding pins the shared command-parameter semantics the
// coverage request must not change: the strict decode, the typed argument
// rejections and the dispatch of a well-formed request.
func TestCallJSONParameterDecoding(t *testing.T) {
	binding := &Binding{manager: fakeManager{}}
	tests := []struct {
		name          string
		method        string
		params        string
		code          string
		messagePrefix string
		ok            bool
	}{
		{"unknown parameter field", "Snapshot", `{"extra":1}`, string(sessionapi.FailureInvalidArgument), "command parameters are invalid", false},
		{"wrong parameter type", "Snapshot", `{"index":"x"}`, string(sessionapi.FailureInvalidArgument), "command parameters are invalid", false},
		{"non-JSON parameters", "Snapshot", `not json`, string(sessionapi.FailureInvalidArgument), "command parameters are invalid", false},
		{"truncated parameters", "Snapshot", `{"session_id":`, string(sessionapi.FailureInvalidArgument), "command parameters are invalid", false},
		{"zero stop generation", "Stop", `{"generation":0}`, string(sessionapi.FailureStaleGeneration), "generation must be positive", false},
		{"negative stop generation", "Stop", `{"generation":-1}`, string(sessionapi.FailureStaleGeneration), "generation must be positive", false},
		{"negative selected index", "Start", `{"index":-1}`, string(sessionapi.FailureInvalidArgument), "profile index must be non-negative", false},
		{"negative configure sequence", "Configure", `{"expected_sequence":-1}`, string(sessionapi.FailureInvalidArgument), "configuration sequence must be non-negative", false},
		{"unknown method", "Nope", `{}`, string(sessionapi.FailureInvalidArgument), "unknown command", false},
		{"snapshot dispatch", "Snapshot", `{}`, "", "", true},
		{"auto mode accepts a negative index", "Start", `{"mode":"` + string(sessionapi.AutoSelect) + `","index":-1}`, "", "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := binding.CallJSON(context.Background(), test.method, []byte(test.params))
			decoded := decodeResponse(t, response)
			if test.ok {
				if !decoded.OK || decoded.Error != nil {
					t.Fatalf("response = %s", response)
				}
				return
			}
			if decoded.OK || decoded.Error == nil {
				t.Fatalf("response = %s", response)
			}
			if decoded.Error.Code != test.code || !strings.HasPrefix(decoded.Error.Message, test.messagePrefix) {
				t.Fatalf("error = %s: %s, want %s: %s", decoded.Error.Code, decoded.Error.Message, test.code, test.messagePrefix)
			}
		})
	}
}
