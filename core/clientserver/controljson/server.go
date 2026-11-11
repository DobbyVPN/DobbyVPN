// Package controljson provides the one-request JSON protocol used by native
// desktop frontends over the authenticated local socket or named pipe.
package controljson

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"core/log"
	"core/sessionapi/mobilebinding"
	"core/sessionapi/wire"
)

const (
	maxRequestBytes     = 8 << 20
	controlCategory     = "CONTROL"
	invalidArgumentCode = "INVALID_ARGUMENT"
)

type Request struct {
	Method            string          `json:"method"`
	Params            json.RawMessage `json:"params"`
	RequestID         string          `json:"request_id,omitempty"`
	DiagnosticSampled *bool           `json:"diagnostic_sampled,omitempty"`
}

// snapshotSampler bounds legacy-client Snapshot diagnostics with a monotonic clock.
type snapshotSampler struct {
	mu       sync.Mutex
	last     time.Time
	now      func() time.Time
	interval time.Duration
}

func (s *snapshotSampler) take() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	interval := s.interval
	if interval == 0 {
		interval = 30 * time.Second
	}
	if !s.last.IsZero() && now.Sub(s.last) >= 0 && now.Sub(s.last) < interval {
		return false
	}
	s.last = now
	return true
}

var legacySnapshotSamples = snapshotSampler{now: time.Now}
var nativeSnapshotSamples = snapshotSampler{now: time.Now, interval: time.Second}

func requestIdentity(id string) string {
	if id != "" && len(id) <= 64 {
		valid := true
		for _, character := range id {
			if (character < '0' || character > '9') && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && character != '-' && character != '_' {
				valid = false
				break
			}
		}
		if valid {
			return id
		}
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "backend-" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("backend-%d", time.Now().UnixNano())
}
func requestFields(id, method string, params json.RawMessage) map[string]any {
	fields := map[string]any{"request_id": id, "method": method}
	var identity struct {
		SessionID        string `json:"session_id"`
		Generation       *int64 `json:"generation"`
		ExpectedSequence *int64 `json:"expected_sequence"`
	}
	if json.Unmarshal(params, &identity) == nil {
		if identity.SessionID != "" && len(identity.SessionID) <= 128 {
			fields["session_id"] = identity.SessionID
		}
		if identity.Generation != nil {
			fields["generation"] = *identity.Generation
		}
		if identity.ExpectedSequence != nil {
			fields["expected_sequence"] = *identity.ExpectedSequence
		}
	}
	return fields
}
func controlFailure(fields map[string]any, phase string, err error) {
	fields["phase"] = phase
	fields["error"] = err.Error()
	if routineDisconnect(err) {
		log.Debug(controlCategory, "desktop command failed", fields)
	} else {
		log.Warn(controlCategory, "desktop command failed", fields)
	}
}

type Handler struct{ Binding *mobilebinding.Binding }

func (h Handler) ServeConn(conn net.Conn) (resultErr error) {
	if h.Binding == nil {
		err := errors.New("desktop control binding is unavailable")
		controlFailure(requestFields(requestIdentity(""), "", nil), "binding", err)
		return err
	}
	var diagnosticFields map[string]any
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			if diagnosticFields == nil {
				diagnosticFields = requestFields(requestIdentity(""), "", nil)
			}
			controlFailure(diagnosticFields, "close", closeErr)
			resultErr = errors.Join(resultErr, fmt.Errorf("close desktop control connection: %w", closeErr))
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		controlFailure(requestFields(requestIdentity(""), "", nil), "deadline", err)
		return fmt.Errorf("set desktop control deadline: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 4096)
	requestBytes, err := readLineBounded(reader, maxRequestBytes)
	if err != nil {
		controlFailure(requestFields(requestIdentity(""), "", nil), "read", err)
		return writeFailureWithCause(conn, invalidArgumentCode, "command request could not be read: "+err.Error(), err)
	}
	var request Request
	if decodeErr := json.Unmarshal(requestBytes, &request); decodeErr != nil {
		controlFailure(requestFields(requestIdentity(""), "", nil), "decode", decodeErr)
		return writeFailureWithCause(conn, invalidArgumentCode, "command request is invalid: "+decodeErr.Error(), decodeErr)
	}
	if request.Method == "" || len(request.Params) == 0 || string(request.Params) == "null" {
		fields := requestFields(requestIdentity(request.RequestID), request.Method, request.Params)
		diagnosticFields = fields
		controlFailure(fields, "validate", errors.New("command method and parameters are required"))
		return writeFailure(conn, invalidArgumentCode, "command method and parameters are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	id := requestIdentity(request.RequestID)
	fields := requestFields(id, request.Method, request.Params)
	diagnosticFields = fields
	sampled := sampleRequest(request)
	started := time.Now()
	if sampled {
		log.Info(controlCategory, "desktop command accepted", fields)
	}
	response := json.RawMessage(h.Binding.CallJSON(ctx, request.Method, request.Params))
	var checked json.RawMessage
	if decodeErr := json.Unmarshal(response, &checked); decodeErr != nil {
		controlFailure(fields, "response-decode", decodeErr)
		return writeFailureWithCause(conn, "INTERNAL", "desktop control returned an invalid response: "+decodeErr.Error(), decodeErr)
	}
	var outcome struct {
		OK    bool          `json:"ok"`
		Error *wire.Failure `json:"error"`
	}
	_ = json.Unmarshal(response, &outcome)
	if outcome.Error != nil {
		controlFailure(fields, "command", fmt.Errorf("%s: %s", outcome.Error.Code, outcome.Error.Message))
	} else if !outcome.OK {
		controlFailure(fields, "command", errors.New("desktop control returned a failure without details"))
	}
	_, err = conn.Write(append(response, '\n'))
	if err != nil {
		controlFailure(fields, "write", err)
		return err
	}
	if sampled && outcome.OK && outcome.Error == nil {
		fields["elapsed_ms"] = time.Since(started).Milliseconds()
		log.Info(controlCategory, "desktop command completed", fields)
	}
	return nil
}

func sampleRequest(request Request) bool {
	if request.Method != "Snapshot" {
		return true
	}
	if request.DiagnosticSampled != nil {
		return *request.DiagnosticSampled && nativeSnapshotSamples.take()
	}
	return legacySnapshotSamples.take()
}

func writeFailureWithCause(conn net.Conn, code, message string, cause error) error {
	if writeErr := writeFailure(conn, code, message); writeErr != nil {
		return errors.Join(cause, writeErr)
	}
	return nil
}

func readLineBounded(reader *bufio.Reader, limit int) ([]byte, error) {
	line := make([]byte, 0, 4096)
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > limit {
			return nil, fmt.Errorf("command request exceeds %d bytes", limit)
		}
		line = append(line, part...)
		if err == nil {
			return line[:len(line)-1], nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return line, nil
			}
			return nil, err
		}
	}
}

func writeFailure(conn net.Conn, code, message string) error {
	response, err := json.Marshal(wire.Response[any]{Error: &wire.Failure{Code: code, Message: message}})
	if err != nil {
		return err
	}
	_, err = conn.Write(append(response, '\n'))
	return err
}
