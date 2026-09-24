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

	"go_module/sessionapi/mobilebinding"
)

const maxRequestBytes = 8 << 20

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Handler struct{ Binding *mobilebinding.Binding }

func (h Handler) ServeConn(conn net.Conn) error {
	if h.Binding == nil {
		return errors.New("desktop control binding is unavailable")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))

	reader := bufio.NewReaderSize(conn, 4096)
	requestBytes, err := readLineBounded(reader, maxRequestBytes)
	if err != nil {
		return writeFailure(conn, "INVALID_ARGUMENT", "command request is missing or too large")
	}
	var request Request
	if err := json.Unmarshal(requestBytes, &request); err != nil {
		return writeFailure(conn, "INVALID_ARGUMENT", "command request is invalid")
	}
	if request.Method == "" || len(request.Params) == 0 || string(request.Params) == "null" {
		return writeFailure(conn, "INVALID_ARGUMENT", "command method and parameters are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response := json.RawMessage(h.Binding.CallJSON(ctx, request.Method, request.Params))
	if !json.Valid(response) {
		return writeFailure(conn, "INTERNAL", "desktop control returned an invalid response")
	}
	_, err = conn.Write(append(response, '\n'))
	return err
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
