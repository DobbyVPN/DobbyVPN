package controljson

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"core/sessionapi/wire"
)

type DialFunc func(context.Context) (net.Conn, error)

// Client sends one command over one authenticated local connection.
type Client struct{ Dial DialFunc }

type CallError struct {
	Code    string
	Message string
}

func (e *CallError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (c Client) Call(ctx context.Context, method string, params, result any) (callErr error) {
	if c.Dial == nil {
		return errors.New("desktop control dialer is unavailable")
	}
	conn, err := c.Dial(ctx)
	if err != nil {
		return err
	}
	// Closing this one-request connection interrupts both reads and writes.
	closeConn := sync.OnceValue(conn.Close)
	stopClose := context.AfterFunc(ctx, func() { _ = closeConn() })
	defer func() {
		stopClose()
		if callErr != nil && ctx.Err() != nil {
			callErr = errors.Join(ctx.Err(), callErr)
		}
		if closeErr := closeConn(); closeErr != nil {
			callErr = errors.Join(callErr, fmt.Errorf("close desktop control connection: %w", closeErr))
		}
	}()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Minute)
	}
	if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
		return fmt.Errorf("set desktop control deadline: %w", deadlineErr)
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode desktop control parameters: %w", err)
	}
	request, err := json.Marshal(Request{Method: method, Params: paramsJSON})
	if err != nil {
		return fmt.Errorf("encode desktop control request: %w", err)
	}
	if _, writeErr := conn.Write(append(request, '\n')); writeErr != nil {
		return fmt.Errorf("send desktop control request: %w", writeErr)
	}
	line, err := readLineBounded(bufio.NewReaderSize(conn, 4096), maxRequestBytes)
	if err != nil {
		return fmt.Errorf("read desktop control response: %w", err)
	}
	var response wire.Response[json.RawMessage]
	if err := json.Unmarshal(line, &response); err != nil {
		return fmt.Errorf("decode desktop control response: %w", err)
	}
	if !response.OK {
		if response.Error == nil {
			return errors.New("desktop control returned a failure without details")
		}
		return &CallError{Code: response.Error.Code, Message: response.Error.Message}
	}
	if result != nil {
		if len(response.Result) == 0 {
			return errors.New("desktop control returned no result")
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode desktop control result: %w", err)
		}
	}
	return nil
}

// CallStream runs the one-request exchange for a streaming method and returns
// the declared tail as a reader of exactly result.Bytes bytes over this call's
// connection. The caller must consume or close the reader; closing it closes
// the connection. Reading later is expected: the service blocks in socket flow
// control until the tail is consumed.
func (c Client) CallStream(ctx context.Context, method string, params any) (result StreamResult, tail io.ReadCloser, callErr error) {
	if c.Dial == nil {
		return result, nil, errors.New("desktop control dialer is unavailable")
	}
	conn, err := c.Dial(ctx)
	if err != nil {
		return result, nil, err
	}
	// Closing this one-request connection interrupts both reads and writes.
	closeConn := sync.OnceValue(conn.Close)
	stopClose := context.AfterFunc(ctx, func() { _ = closeConn() })
	handedOff := false
	defer func() {
		if handedOff {
			return
		}
		stopClose()
		if callErr != nil && ctx.Err() != nil {
			callErr = errors.Join(ctx.Err(), callErr)
		}
		if closeErr := closeConn(); closeErr != nil {
			callErr = errors.Join(callErr, fmt.Errorf("close desktop control connection: %w", closeErr))
		}
	}()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(2 * time.Minute)
	}
	if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
		return result, nil, fmt.Errorf("set desktop control deadline: %w", deadlineErr)
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return result, nil, fmt.Errorf("encode desktop control parameters: %w", err)
	}
	request, err := json.Marshal(Request{Method: method, Params: paramsJSON})
	if err != nil {
		return result, nil, fmt.Errorf("encode desktop control request: %w", err)
	}
	if _, writeErr := conn.Write(append(request, '\n')); writeErr != nil {
		return result, nil, fmt.Errorf("send desktop control request: %w", writeErr)
	}
	// The tail follows the response line on the same stream: read it through
	// the same reader so bytes the line read already buffered are not lost.
	reader := bufio.NewReaderSize(conn, 4096)
	line, err := readLineBounded(reader, maxRequestBytes)
	if err != nil {
		return result, nil, fmt.Errorf("read desktop control response: %w", err)
	}
	var response wire.Response[StreamResult]
	if err := json.Unmarshal(line, &response); err != nil {
		return result, nil, fmt.Errorf("decode desktop control response: %w", err)
	}
	if !response.OK {
		if response.Error == nil {
			return result, nil, errors.New("desktop control returned a failure without details")
		}
		return result, nil, &CallError{Code: response.Error.Code, Message: response.Error.Message}
	}
	handedOff = true
	return response.Result, &streamTail{reader: io.LimitReader(reader, response.Result.Bytes), stop: stopClose, close: closeConn}, nil
}

// streamTail reads the declared response tail and owns its connection.
type streamTail struct {
	reader io.Reader
	stop   func() bool
	close  func() error
}

func (tail *streamTail) Read(p []byte) (int, error) { return tail.reader.Read(p) }

func (tail *streamTail) Close() error {
	tail.stop()
	return tail.close()
}
