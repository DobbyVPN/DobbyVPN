package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/cbeuw/Cloak/internal/common"

	mux "github.com/cbeuw/Cloak/internal/multiplex"
	log "github.com/sirupsen/logrus"
)

const connectionRetryInterval = 3 * time.Second

type sessionConnectionResult struct {
	conn       net.Conn
	sessionKey [32]byte
	err        error
}

// On different invocations to MakeSession, authInfo.SessionId MUST be different.
func MakeSession(ctx context.Context, connConfig RemoteConnConfig, authInfo AuthInfo, dialer common.Dialer) (*mux.Session, error) {
	return makeSession(ctx, connConfig, authInfo, dialer, func(config TransportConfig) Transport {
		return config.CreateTransport()
	})
}

func makeSession(
	ctx context.Context,
	connConfig RemoteConnConfig,
	authInfo AuthInfo,
	dialer common.Dialer,
	createTransport func(TransportConfig) Transport,
) (*mux.Session, error) {
	if ctx == nil {
		return nil, errors.New("session context is nil")
	}
	if connConfig.NumConn <= 0 {
		return nil, fmt.Errorf("invalid underlying connection count %d", connConfig.NumConn)
	}
	log.Info("Attempting to start a new session")

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()
	results := make(chan sessionConnectionResult, connConfig.NumConn)
	for i := 0; i < connConfig.NumConn; i++ {
		transportConfig := connConfig.Transport
		go func() {
			results <- establishSessionConnection(workerCtx, connConfig, authInfo, dialer, transportConfig, createTransport)
		}()
	}

	connections := make([]net.Conn, 0, connConfig.NumConn)
	var sessionKey [32]byte
	var resultErr error
	for i := 0; i < connConfig.NumConn; i++ {
		result := <-results
		if result.err != nil {
			if resultErr == nil {
				resultErr = result.err
				cancelWorkers()
			}
			continue
		}
		if len(connections) == 0 {
			sessionKey = result.sessionKey
		} else if result.sessionKey != sessionKey {
			_ = result.conn.Close()
			if resultErr == nil {
				resultErr = errors.New("underlying connections returned different session keys")
				cancelWorkers()
			}
			continue
		}
		connections = append(connections, result.conn)
	}
	if err := ctx.Err(); err != nil {
		resultErr = err
	}
	if resultErr != nil {
		closeConnections(connections)
		return nil, resultErr
	}
	log.Debug("All underlying connections established")

	obfuscator, err := mux.MakeObfuscator(authInfo.EncryptionMethod, sessionKey)
	if err != nil {
		closeConnections(connections)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		closeConnections(connections)
		return nil, err
	}

	seshConfig := mux.SessionConfig{
		Singleplex:         connConfig.Singleplex,
		Obfuscator:         obfuscator,
		Valve:              nil,
		Unordered:          authInfo.Unordered,
		MsgOnWireSizeLimit: appDataMaxLength,
	}
	sesh := mux.MakeSession(authInfo.SessionId, seshConfig)

	for _, conn := range connections {
		sesh.AddConnection(conn)
	}

	log.Infof("Session %v established", authInfo.SessionId)
	return sesh, nil
}

func establishSessionConnection(
	ctx context.Context,
	connConfig RemoteConnConfig,
	authInfo AuthInfo,
	dialer common.Dialer,
	transportConfig TransportConfig,
	createTransport func(TransportConfig) Transport,
) sessionConnectionResult {
	for {
		if err := ctx.Err(); err != nil {
			return sessionConnectionResult{err: err}
		}
		transportConn := createTransport(transportConfig)
		if transportConn == nil {
			return sessionConnectionResult{err: errors.New("transport factory returned nil")}
		}
		remoteConn, err := dialer.DialContext(ctx, "tcp", connConfig.RemoteAddr)
		if err != nil {
			if ctx.Err() != nil {
				return sessionConnectionResult{err: ctx.Err()}
			}
			log.Errorf("Failed to establish new connections to remote: %v", err)
			if !waitForSessionRetry(ctx) {
				return sessionConnectionResult{err: ctx.Err()}
			}
			continue
		}

		cancelCloseDone := make(chan struct{})
		stopCancelClose := context.AfterFunc(ctx, func() {
			_ = remoteConn.Close()
			close(cancelCloseDone)
		})
		sessionKey, err := transportConn.Handshake(remoteConn, authInfo)
		if !stopCancelClose() {
			<-cancelCloseDone
		}
		if err != nil {
			_ = transportConn.Close()
			_ = remoteConn.Close()
			if ctx.Err() != nil {
				return sessionConnectionResult{err: ctx.Err()}
			}
			log.Errorf("Failed to prepare connection to remote: %v", err)

			// In Cloak v2.11.0, we've updated uTLS version and subsequently increased the first packet size for chrome above 1500
			// https://github.com/cbeuw/Cloak/pull/306#issuecomment-2862728738. As a backwards compatibility feature, if we fail
			// to connect using chrome signature, retry with firefox which has a smaller packet size.
			if transportConfig.mode == "direct" && transportConfig.browser == chrome {
				transportConfig.browser = firefox
				log.Warnf("failed to connect with chrome signature, falling back to retry with firefox")
			}
			if !waitForSessionRetry(ctx) {
				return sessionConnectionResult{err: ctx.Err()}
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			_ = transportConn.Close()
			return sessionConnectionResult{err: err}
		}
		return sessionConnectionResult{conn: transportConn, sessionKey: sessionKey}
	}
}

func waitForSessionRetry(ctx context.Context) bool {
	timer := time.NewTimer(connectionRetryInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func closeConnections(conns []net.Conn) {
	for _, conn := range conns {
		_ = conn.Close()
	}
}
