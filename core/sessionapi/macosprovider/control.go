// Package macosprovider connects the desktop session owner to the Go runtime
// inside the installed macOS packet-tunnel system extension.
package macosprovider

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"core/iprange"
	"core/protocol"
	"core/sessionapi"
)

const Directory = "/Library/Application Support/DobbyVPN/Protection"

func init() {
	// The normalized Xray and TrustTunnel configurations have nested JSON
	// values. Gob retains the Go number types the native protocol expects.
	gob.Register(map[string]any{})
	gob.Register([]any{})
}

func socketPath(key string) (string, error) {
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 16 || key != strings.ToLower(key) {
		return "", errors.New("invalid provider connection identity")
	}
	return filepath.Join(Directory, key+".sock"), nil
}

type operation struct {
	Action   string
	Ref      sessionapi.SessionRef
	Instance string
	Summary  sessionapi.ProfileSummary
	Config   protocol.Config
	Excluded []iprange.Range
	Hosts    []string
	Reason   string
}

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
	defer reply.Body.Close()
	data, err := io.ReadAll(reply.Body)
	if err != nil {
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
