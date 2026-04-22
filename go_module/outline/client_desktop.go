//go:build !(android || ios)

package outline

import (
	"context"
	"errors"
	"fmt"
	"go_module/common"
	"go_module/log"
	outlineCommon "go_module/outline/common"
	"go_module/outline/internal"
	"net"
	"strings"
	"sync"
	"time"
)

type OutlineClient struct {
	app    *internal.App
	cancel func()

	mu sync.Mutex
}

func NewClient(transportConfig string) *OutlineClient {
	cfg := common.GetNetworkConfig()

	c := &OutlineClient{
		app: &internal.App{
			TransportConfig: &transportConfig,
			RoutingConfig: &internal.RoutingConfig{
				TunDeviceName:        "outline233",
				TunDeviceIP:          cfg.TunDevice,
				TunDeviceMTU:         1500,
				TunGatewayCIDR:       cfg.TunGateway + "/32",
				RoutingTableID:       233,
				RoutingTablePriority: 23333,
				DNSServerIP:          "9.9.9.9",
			},
		},
	}
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	// Channel to receive initialization result from the goroutine
	initResult := make(chan error, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("outline crashed: %v", r)
				log.Infof("outline goroutine recovered from panic: %v", err)
				select {
				case initResult <- err:
				default:
				}
				common.Client.MarkInactive(outlineCommon.Name)
			}
		}()
		err := c.app.Run(ctx, initResult)
		if err != nil {
			log.Infof("connect outline failed: %v", err)
			common.Client.MarkInactive(outlineCommon.Name)
		}
	}()

	// Wait for initialization result with timeout
	select {
	case err := <-initResult:
		if err != nil {
			c.cancel()
			c.cancel = nil
			return fmt.Errorf("failed to initialize outline connection: %w", err)
		}
		log.Infof("Outline connection initialized successfully")
		common.Client.MarkActive(outlineCommon.Name)
		return nil
	case <-time.After(30 * time.Second):
		c.cancel()
		c.cancel = nil
		return fmt.Errorf("timeout waiting for outline connection initialization")
	}
}

func (c *OutlineClient) Disconnect() error {
	log.Infof("Disconnect: try to lock c.mu")
	c.mu.Lock()
	defer c.mu.Unlock()
	log.Infof("Disconnect: locked c.mu")

	if c.cancel != nil {
		log.Infof("Disconnect: c.cancel != nil")
		c.cancel()
		c.cancel = nil
	}
	log.Infof("Disconnect: common.Client.MarkInactive")
	common.Client.MarkInactive(outlineCommon.Name)
	log.Infof("Disconnect: MarkedInactive")
	return nil
}

func (c *OutlineClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}

func (c *OutlineClient) HealthCheck() error {
	serverIp, err := internal.ResolveServerIPFromConfig(*c.app.TransportConfig)
	if err != nil {
		return fmt.Errorf("Failed resolve server ip: %v", err)
	}

	log.Infof("[HC] Resolves server ip: %v", serverIp)

	return nil
}

func checkServerAlive(address string, port int) error {
	target := fmt.Sprintf("%s:%d", address, port)

	d := net.Dialer{
		Timeout:   1 * time.Second,
		KeepAlive: -1,
	}

	var lastErr error

	for i := 0; i < 3; i++ {
		conn, err := d.Dial("tcp", target)
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "refused") {
				return err
			}
			continue
		}

		func() {
			defer func() { _ = conn.Close() }()

			_ = conn.SetDeadline(time.Now().Add(1 * time.Second))

			// We intentionally perform both Write and Read in addition to Dial.
			//
			// On iOS and mobile networks, a successful TCP connect (Dial) does NOT
			// necessarily mean that the remote service is actually listening.
			// The connection may be accepted optimistically by a proxy, NAT, or
			// SYN-proxy before reaching the real server.
			//
			// Write() alone is also insufficient: it only confirms that the OS
			// accepted data into a local send buffer, not that the server received it.
			//
			// Read() forces TCP to resolve the real connection state:
			//   - timeout  -> the server is alive but silent (acceptable)
			//   - data     -> the server is alive and responding
			//   - EOF/RST  -> the connection was not actually accepted by the service
			//
			// This sequence minimizes false-positive "alive" results caused by
			// optimistic TCP connections.

			if _, writeErr := conn.Write([]byte{0x00}); writeErr != nil {
				lastErr = writeErr
				return
			}

			buf := make([]byte, 1)
			_, readErr := conn.Read(buf)
			if readErr != nil {
				var ne net.Error
				if errors.As(readErr, &ne) && ne.Timeout() {
					lastErr = nil
					return
				}

				lastErr = readErr
				return
			}

			lastErr = nil
		}()

		if lastErr == nil {
			return nil
		}
	}

	return lastErr
}
