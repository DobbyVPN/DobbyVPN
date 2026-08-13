package client

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/cbeuw/Cloak/internal/common"

	mux "github.com/cbeuw/Cloak/internal/multiplex"
	log "github.com/sirupsen/logrus"
)

func RouteUDP(ctx context.Context, bindFunc func() (*net.UDPConn, error), streamTimeout time.Duration, singleplex bool, newSeshFunc func() (*mux.Session, error)) {
	var sesh *mux.Session
	localConn, err := bindFunc()
	if err != nil {
		log.Errorf("Failed to bind UDP proxy listener: %v", err)
		return
	}
	stopCancelClose := context.AfterFunc(ctx, func() { _ = localConn.Close() })
	defer stopCancelClose()

	streams := make(map[string]*mux.Stream)
	var streamsMutex sync.Mutex
	var streamWorkers sync.WaitGroup
	defer streamWorkers.Wait()

	data := make([]byte, 8192)
	for {
		i, addr, err := localConn.ReadFrom(data)
		if err != nil {
			if isClosedConnError(err) {
				return
			}
			log.Errorf("Failed to read first packet from proxy client: %v", err)
			continue
		}

		if !singleplex && (sesh == nil || sesh.IsClosed()) {
			if sesh != nil {
				log.Infof("Replacing closed Cloak session cause=%s", sesh.TerminalCause())
			}
			sesh, err = newSeshFunc()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Errorf("Failed to establish proxy session: %v", err)
				continue
			}
		}

		streamsMutex.Lock()
		stream, ok := streams[addr.String()]
		if !ok {
			if singleplex {
				sesh, err = newSeshFunc()
				if err != nil {
					streamsMutex.Unlock()
					if ctx.Err() != nil {
						return
					}
					log.Errorf("Failed to establish proxy session: %v", err)
					continue
				}
			}

			stream, err = sesh.OpenStream()
			if err != nil {
				if singleplex {
					sesh.Close()
				}
				log.Errorf("Failed to open stream: %v", err)
				streamsMutex.Unlock()
				continue
			}
			streams[addr.String()] = stream
			streamsMutex.Unlock()

			_ = stream.SetReadDeadline(time.Now().Add(streamTimeout))

			proxyAddr := addr
			streamWorkers.Add(1)
			go func(stream *mux.Stream, localConn *net.UDPConn) {
				defer streamWorkers.Done()
				buf := make([]byte, 8192)
				for {
					n, err := stream.Read(buf)
					if err != nil {
						log.Tracef("copying stream to proxy client: %v", err)
						break
					}
					_ = stream.SetReadDeadline(time.Now().Add(streamTimeout))

					_, err = localConn.WriteTo(buf[:n], proxyAddr)
					if err != nil {
						log.Tracef("copying stream to proxy client: %v", err)
						break
					}
				}
				streamsMutex.Lock()
				delete(streams, addr.String())
				streamsMutex.Unlock()
				stream.Close()
				return
			}(stream, localConn)
		} else {
			streamsMutex.Unlock()
		}

		_, err = stream.Write(data[:i])
		if err != nil {
			log.Tracef("copying proxy client to stream: %v", err)
			streamsMutex.Lock()
			delete(streams, addr.String())
			streamsMutex.Unlock()
			stream.Close()
			continue
		}
		_ = stream.SetReadDeadline(time.Now().Add(streamTimeout))
	}
}

func isClosedConnError(err error) bool {
	return errors.Is(err, net.ErrClosed) || strings.Contains(strings.ToLower(err.Error()), "use of closed network connection")
}

func RouteTCP(ctx context.Context, listener net.Listener, streamTimeout time.Duration, singleplex bool, newSeshFunc func() (*mux.Session, error)) {
	var sesh *mux.Session
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		localConn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || isClosedConnError(err) {
				return
			}
			log.Errorf("Failed to accept proxy client: %v", err)
			continue
		}
		if !singleplex && (sesh == nil || sesh.IsClosed()) {
			if sesh != nil {
				log.Infof("Replacing closed Cloak session cause=%s", sesh.TerminalCause())
			}
			sesh, err = newSeshFunc()
			if err != nil {
				_ = localConn.Close()
				if ctx.Err() != nil {
					return
				}
				log.Errorf("Failed to establish proxy session: %v", err)
				continue
			}
		}
		workers.Add(1)
		go func(sesh *mux.Session, localConn net.Conn, timeout time.Duration) {
			defer workers.Done()
			stopCancelClose := context.AfterFunc(ctx, func() { _ = localConn.Close() })
			defer stopCancelClose()
			if singleplex {
				var err error
				sesh, err = newSeshFunc()
				if err != nil {
					if ctx.Err() == nil {
						log.Errorf("Failed to establish proxy session: %v", err)
					}
					_ = localConn.Close()
					return
				}
			}

			data := make([]byte, 10240)
			_ = localConn.SetReadDeadline(time.Now().Add(streamTimeout))
			i, err := io.ReadAtLeast(localConn, data, 1)
			if err != nil {
				log.Errorf("Failed to read first packet from proxy client: %v", err)
				localConn.Close()
				return
			}
			var zeroTime time.Time
			_ = localConn.SetReadDeadline(zeroTime)

			stream, err := sesh.OpenStream()
			if err != nil {
				log.Errorf("Failed to open stream: %v", err)
				localConn.Close()
				if singleplex {
					sesh.Close()
				}
				return
			}

			_, err = stream.Write(data[:i])
			if err != nil {
				log.Errorf("Failed to write to stream: %v", err)
				localConn.Close()
				stream.Close()
				return
			}

			go func() {
				if _, err := common.Copy(localConn, stream); err != nil {
					log.Tracef("copying stream to proxy client: %v", err)
				}
			}()
			if _, err = common.Copy(stream, localConn); err != nil {
				log.Tracef("copying proxy client to stream: %v", err)
			}
		}(sesh, localConn, streamTimeout)
	}
}
