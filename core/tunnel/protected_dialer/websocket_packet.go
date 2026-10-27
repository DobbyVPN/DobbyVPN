package protected_dialer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.getoutline.org/sdk/transport"
	"golang.getoutline.org/sdk/x/configurl"
)

// Packet reads must return one whole WebSocket message, including all of its
// frames and TCP reads, before Shadowsocks authenticates the datagram.
func registerOutlineWebsocketPackets(providers *configurl.ProviderContainer) {
	providers.PacketDialers.RegisterType("ws", func(ctx context.Context, config *configurl.Config) (transport.PacketDialer, error) {
		stream, err := providers.StreamDialers.NewInstance(ctx, config.BaseConfig)
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
			case "udp_path":
				path = entries[0]
			default:
				return nil, fmt.Errorf("unsupported WebSocket option %s", key)
			}
		}
		if path == "" {
			return nil, errors.New("must specify udp_path")
		}
		return transport.FuncPacketDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return dialWebsocketPacket(ctx, stream, address, path)
		}), nil
	})
}

func dialWebsocketPacket(ctx context.Context, stream transport.StreamDialer, address, path string) (net.Conn, error) {
	var wire *packetWebsocketWire
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := stream.DialStream(ctx, address)
		if err != nil {
			return nil, err
		}
		wire = &packetWebsocketWire{Conn: conn}
		return wire, nil
	}}
	endpoint := url.URL{Scheme: "ws", Host: address, Path: path}
	headers := http.Header{"User-Agent": {fmt.Sprintf("Outline (%s; %s; %s)", runtime.GOOS, runtime.GOARCH, runtime.Version())}}
	conn, response, err := dialer.DialContext(ctx, endpoint.String(), headers)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	conn.SetReadLimit(65535)
	return &websocketPacketConn{websocket: conn, wire: wire}, nil
}

// Gorilla installs its own write deadlines before each frame. Compose those
// with the application's net.Conn deadline rather than clearing that deadline.
// This mutex covers only deadline changes, so a setter can interrupt a blocked
// Write without waiting for the packet write mutex.
type packetWebsocketWire struct {
	net.Conn
	mu                       sync.Mutex
	applicationWriteDeadline time.Time
	transportWriteDeadline   time.Time
}

func (w *packetWebsocketWire) SetWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.transportWriteDeadline = deadline
	return w.applyWriteDeadline()
}

func (w *packetWebsocketWire) setApplicationWriteDeadline(deadline time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.applicationWriteDeadline = deadline
	return w.applyWriteDeadline()
}

func (w *packetWebsocketWire) applyWriteDeadline() error {
	deadline := w.transportWriteDeadline
	if application := w.applicationWriteDeadline; !application.IsZero() && (deadline.IsZero() || application.Before(deadline)) {
		deadline = application
	}
	return w.Conn.SetWriteDeadline(deadline)
}

type websocketPacketConn struct {
	websocket       *websocket.Conn
	wire            *packetWebsocketWire
	readMu, writeMu sync.Mutex
}

func (c *websocketPacketConn) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	kind, message, err := c.websocket.ReadMessage()
	if err != nil {
		var closed *websocket.CloseError
		if errors.As(err, &closed) && closed.Code == websocket.CloseNormalClosure {
			return 0, io.EOF
		}
		return 0, err
	}
	if kind != websocket.BinaryMessage {
		return 0, errors.New("WebSocket packet is not binary")
	}
	if len(message) > len(buffer) {
		return 0, io.ErrShortBuffer
	}
	return copy(buffer, message), nil
}

func (c *websocketPacketConn) Write(packet []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.websocket.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		return 0, err
	}
	return len(packet), nil
}

func (c *websocketPacketConn) LocalAddr() net.Addr  { return c.wire.LocalAddr() }
func (c *websocketPacketConn) RemoteAddr() net.Addr { return c.wire.RemoteAddr() }
func (c *websocketPacketConn) SetReadDeadline(deadline time.Time) error {
	return c.wire.SetReadDeadline(deadline)
}
func (c *websocketPacketConn) SetWriteDeadline(deadline time.Time) error {
	return c.wire.setApplicationWriteDeadline(deadline)
}
func (c *websocketPacketConn) SetDeadline(deadline time.Time) error {
	return errors.Join(c.SetReadDeadline(deadline), c.SetWriteDeadline(deadline))
}
func (c *websocketPacketConn) Close() error { return c.websocket.Close() }
