// Package controljson provides the one-request JSON protocol used by native
// desktop frontends over the authenticated local socket or named pipe.
package controljson

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"core/sessionapi/mobilebinding"
)

const maxRequestBytes = 8 << 20

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Handler struct{ Binding *mobilebinding.Binding }

func (h Handler) ServeConn(conn net.Conn) (resultErr error) {
	if h.Binding == nil {
		return errors.New("desktop control binding is unavailable")
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close desktop control connection: %w", closeErr))
		}
	}()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Minute)); err != nil {
		return fmt.Errorf("set desktop control deadline: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 4096)
	requestBytes, err := readLineBounded(reader, maxRequestBytes)
	if err != nil {
		return writeFailureWithCause(conn, "INVALID_ARGUMENT", "command request could not be read: "+err.Error(), err)
	}
	var request Request
	if decodeErr := json.Unmarshal(requestBytes, &request); decodeErr != nil {
		return writeFailureWithCause(conn, "INVALID_ARGUMENT", "command request is invalid: "+decodeErr.Error(), decodeErr)
	}
	if request.Method == "" || len(request.Params) == 0 || string(request.Params) == "null" {
		return writeFailure(conn, "INVALID_ARGUMENT", "command method and parameters are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response := json.RawMessage(h.Binding.CallJSON(ctx, request.Method, request.Params))
	var checked json.RawMessage
	if decodeErr := json.Unmarshal(response, &checked); decodeErr != nil {
		return writeFailureWithCause(conn, "INTERNAL", "desktop control returned an invalid response: "+decodeErr.Error(), decodeErr)
	}
	_, err = conn.Write(append(response, '\n'))
	return err
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
	response, _ := json.Marshal(struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{OK: false, Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
	_, err := conn.Write(append(response, '\n'))
	return err
}
