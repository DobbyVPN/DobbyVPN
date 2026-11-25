//go:build darwin && !ios

package macosprovider

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"core/sessionapi"
)

type endpoint struct {
	client *http.Client
}

func newEndpoint(key string) (endpoint, error) {
	path, err := socketPath(key)
	if err != nil {
		return endpoint{}, err
	}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}
	return endpoint{client: &http.Client{Transport: transport}}, nil
}

func (e endpoint) call(ctx context.Context, op operation) (string, error) {
	var body bytes.Buffer
	if err := gob.NewEncoder(&body).Encode(op); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://provider/runtime", &body)
	if err != nil {
		return "", err
	}
	reply, err := e.client.Do(req)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(reply.Body)
	if err := errors.Join(readErr, reply.Body.Close()); err != nil {
		return "", err
	}
	if reply.StatusCode != http.StatusOK {
		err := fmt.Errorf("packet provider %s: %s", op.Action, strings.TrimSpace(string(data)))
		if reply.Header.Get("Dobby-Failure") == string(sessionapi.FailureCleanup) {
			return "", &sessionapi.CleanupFailure{Err: err}
		}
		return "", err
	}
	return string(data), nil
}
