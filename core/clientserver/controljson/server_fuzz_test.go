package controljson

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// FuzzReadLineBounded checks the bounded one-line framing used by desktop
// control requests: it returns the first newline-terminated line without the
// newline (or the whole input at EOF) and rejects any first line longer than
// maxRequestBytes.
func FuzzReadLineBounded(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		[]byte("\n"),
		[]byte("x"),
		[]byte("x\ny\n"),
		append(bytes.Repeat([]byte("a"), 4095), '\n'),
		append(bytes.Repeat([]byte("a"), 4096), '\n'),
		append(bytes.Repeat([]byte("a"), maxRequestBytes-1), '\n'),
		append(bytes.Repeat([]byte("a"), maxRequestBytes), '\n'),
		bytes.Repeat([]byte("a"), maxRequestBytes+1),
		bytes.Repeat([]byte("a"), maxRequestBytes),
		[]byte("a\x00b\nc"),
		[]byte("\n\n\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		reader := bufio.NewReaderSize(bytes.NewReader(data), 4096)
		line, err := readLineBounded(reader, maxRequestBytes)
		index := bytes.IndexByte(data, '\n')
		firstLineLen := len(data)
		if index >= 0 {
			firstLineLen = index + 1
		}
		switch {
		case firstLineLen > maxRequestBytes:
			if err == nil {
				t.Fatalf("line of %d bytes was accepted (limit %d)", firstLineLen, maxRequestBytes)
			}
			if line != nil {
				t.Fatalf("rejected line returned data")
			}
		case len(data) == 0:
			if err == nil {
				t.Fatalf("empty input returned a line")
			}
			if line != nil {
				t.Fatalf("empty input returned data")
			}
		default:
			if err != nil {
				t.Fatalf("line of %d bytes was rejected: %v", firstLineLen, err)
			}
			if len(line) > maxRequestBytes {
				t.Fatalf("line of %d bytes exceeds the limit", len(line))
			}
			want := data
			if index >= 0 {
				want = data[:index]
			}
			if !bytes.Equal(line, want) {
				t.Fatalf("line = %q, want %q", line, want)
			}
		}
	})
}

// FuzzRequestEnvelope checks the one-request JSON envelope and the bounded
// identity fields copied into diagnostics: decoding never panics, and every
// derived request identity and diagnostic field set stays within its
// documented bounds.
func FuzzRequestEnvelope(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{}`),
		[]byte(`{"method":"Snapshot","params":{"session_id":"s"}}`),
		[]byte(`{"method":"Snapshot","params":null}`),
		[]byte(`{"method":"","params":{}}`),
		[]byte(`{"method":"Start","params":{"generation":5},"request_id":"abc-123","diagnostic_sampled":true}`),
		[]byte(`{"request_id":"` + strings.Repeat("a", 65) + `"}`),
		[]byte(`{"request_id":"has spaces"}`),
		[]byte(`{"method":5,"params":[]}`),
		[]byte(`{"method":"X","params":{"session_id":123,"generation":"5"}}`),
		[]byte(`{`),
		[]byte(`[]`),
		[]byte(`{"params":{}}`),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var request Request
		if err := json.Unmarshal(data, &request); err != nil {
			return
		}
		id := requestIdentity(request.RequestID)
		checkRequestIdentity(t, id)
		checkRequestFields(t, requestFields(id, request.Method, request.Params), id, request.Method)
	})
}

// checkRequestIdentity verifies the bounded identity copied into diagnostics:
// a client-supplied id is kept only when it is short and safe, otherwise a
// generated backend identity replaces it.
func checkRequestIdentity(t *testing.T, id string) {
	t.Helper()
	if id == "" {
		t.Fatalf("request identity is empty")
	}
	if len(id) > 64 && !strings.HasPrefix(id, "backend-") {
		t.Fatalf("request identity %q is longer than 64 bytes", id)
	}
	for _, character := range id {
		if (character < '0' || character > '9') && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && character != '-' && character != '_' {
			t.Fatalf("request identity %q carries an unsafe character", id)
		}
	}
}

// checkRequestFields verifies the diagnostic field set and the bounds of the
// identity values copied out of untrusted parameters.
func checkRequestFields(t *testing.T, fields map[string]any, id, method string) {
	t.Helper()
	if got := fields["request_id"]; got != id {
		t.Fatalf("request_id = %v, want %q", got, id)
	}
	if got := fields["method"]; got != method {
		t.Fatalf("method = %v, want %q", got, method)
	}
	for key := range fields {
		switch key {
		case "request_id", "method", "session_id", "generation", "expected_sequence":
		default:
			t.Fatalf("unexpected diagnostic field %q", key)
		}
	}
	if value, present := fields["session_id"]; present {
		text, ok := value.(string)
		if !ok || len(text) == 0 || len(text) > 128 {
			t.Fatalf("session_id diagnostic = %v", value)
		}
	}
	for _, key := range []string{"generation", "expected_sequence"} {
		if value, present := fields[key]; present {
			if _, ok := value.(int64); !ok {
				t.Fatalf("%s diagnostic = %v (%T)", key, value, value)
			}
		}
	}
}
