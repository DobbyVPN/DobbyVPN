package protected_dialer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"

	"golang.getoutline.org/sdk/transport"
	"golang.getoutline.org/sdk/x/configurl"
	"golang.getoutline.org/sdk/x/websocket"
)

// Keep the SDK's framing and half-close behavior. Its endpoint discards failed
// HTTP upgrade responses, so observe the status line at its plaintext boundary.
func registerOutlineWebsocketStreams(providers *configurl.ProviderContainer) {
	providers.StreamDialers.RegisterType("ws", func(ctx context.Context, config *configurl.Config) (transport.StreamDialer, error) {
		base, err := providers.StreamDialers.NewInstance(ctx, config.BaseConfig)
		if err != nil {
			return nil, err
		}
		values, err := url.ParseQuery(config.URL.Opaque)
		if err != nil {
			return nil, err
		}
		var path string
		for key, entries := range values {
			if len(entries) != 1 {
				return nil, fmt.Errorf("WebSocket option %s must have one value", key)
			}
			switch strings.ToLower(key) {
			case "tcp_path":
				path = entries[0]
			case "udp_path":
			default:
				return nil, fmt.Errorf("unsupported WebSocket option %s", key)
			}
		}
		if path == "" {
			return nil, errors.New("must specify tcp_path")
		}
		return transport.FuncStreamDialer(func(ctx context.Context, address string) (transport.StreamConn, error) {
			response := &websocketResponseStatus{}
			endpoint := transport.FuncStreamEndpoint(func(ctx context.Context) (transport.StreamConn, error) {
				conn, err := base.DialStream(ctx, address)
				if err != nil {
					return nil, err
				}
				return transport.WrapConn(conn, io.TeeReader(conn, response), conn), nil
			})
			wsURL := url.URL{Scheme: "ws", Host: address, Path: path}
			connect, err := websocket.NewStreamEndpoint(wsURL.String(), endpoint)
			if err != nil {
				return nil, err
			}
			conn, err := connect(ctx)
			if err != nil {
				return nil, fmt.Errorf("WebSocket stream upgrade response=%q: %w", response.status(), err)
			}
			return conn, nil
		}), nil
	})
}

// Retain only the status line, without buffering or inspecting application data.
// The bound limits diagnostic memory even if a peer never terminates its line.
type websocketResponseStatus struct {
	mu       sync.Mutex
	line     []byte
	complete bool
	overflow bool
}

func (r *websocketResponseStatus) Write(p []byte) (int, error) {
	n := len(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.complete || r.overflow {
		return n, nil
	}
	if end := bytes.IndexByte(p, '\n'); end >= 0 {
		p, r.complete = p[:end+1], true
	}
	if len(r.line)+len(p) > 8192 {
		r.line, r.overflow = nil, true
		return n, nil
	}
	r.line = append(r.line, p...)
	return n, nil
}

func (r *websocketResponseStatus) status() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.overflow {
		return "unavailable: status line exceeded 8192 bytes"
	}
	if !r.complete {
		return "unavailable: no complete HTTP status line"
	}
	return strings.TrimRight(string(r.line), "\r\n")
}
