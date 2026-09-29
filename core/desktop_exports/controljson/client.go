package controljson

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
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

func (c Client) Call(ctx context.Context, method string, params any, result any) error {
	if c.Dial == nil {
		return errors.New("desktop control dialer is unavailable")
	}
	conn, err := c.Dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	}
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode desktop control parameters: %w", err)
	}
	request, err := json.Marshal(Request{Method: method, Params: paramsJSON})
	if err != nil {
		return fmt.Errorf("encode desktop control request: %w", err)
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("send desktop control request: %w", err)
	}
	line, err := readLineBounded(bufio.NewReaderSize(conn, 4096), maxRequestBytes)
	if err != nil {
		return fmt.Errorf("read desktop control response: %w", err)
	}
	var response struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
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
