package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go_module/grpcproto"
)

// GRPCClient adapts the authenticated local service to SessionClient.  It
// remembers only the opaque session ID and revisions; configuration bytes never
// enter this object after Configure returns.
type GRPCClient struct {
	client grpcproto.VpnClient

	mu        sync.RWMutex
	sessionID string
}

func NewGRPCClient(client grpcproto.VpnClient) *GRPCClient {
	return &GRPCClient{client: client}
}

func (c *GRPCClient) session() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sessionID
}

func (c *GRPCClient) remember(snapshot *grpcproto.SessionSnapshot) {
	if snapshot == nil || snapshot.GetSessionId() == "" {
		return
	}
	c.mu.Lock()
	c.sessionID = snapshot.GetSessionId()
	c.mu.Unlock()
}

func (c *GRPCClient) clearSession() {
	c.mu.Lock()
	c.sessionID = ""
	c.mu.Unlock()
}

func (c *GRPCClient) Configure(ctx context.Context, raw []byte, sequence uint64) (ConfigureResult, error) {
	response, err := c.client.Configure(ctx, &grpcproto.SessionConfigureRequest{
		SessionId:        c.session(),
		RawConfig:        append([]byte(nil), raw...),
		ExpectedSequence: sequence,
	})
	if err != nil {
		return ConfigureResult{}, err
	}
	if failure := response.GetFailure(); failure != nil {
		return ConfigureResult{}, rpcFailure(failure)
	}
	return ConfigureResult{
		Digest:     response.GetDigest(),
		Sequence:   response.GetSequence(),
		SourceKind: sourceKind(response.GetSourceKind()),
		Profiles:   profiles(response.GetProfiles()),
		Warnings:   warnings(response.GetWarnings()),
	}, nil
}

func (c *GRPCClient) Start(ctx context.Context, sequence uint64) (StartResult, error) {
	response, err := c.client.Start(ctx, &grpcproto.SessionStartRequest{
		SessionId:        c.session(),
		Mode:             grpcproto.SessionStartMode_SESSION_START_MODE_AUTO_SELECT,
		ExpectedSequence: sequence,
	})
	if err != nil {
		return StartResult{}, err
	}
	if failure := response.GetFailure(); failure != nil {
		return StartResult{}, rpcFailure(failure)
	}
	return StartResult{Generation: response.GetGeneration(), Sequence: response.GetSequence()}, nil
}

func (c *GRPCClient) Stop(ctx context.Context, generation uint64) (StopResult, error) {
	response, err := c.client.Stop(ctx, &grpcproto.SessionStopRequest{
		SessionId:  c.session(),
		Generation: generation,
	})
	if err != nil {
		return StopResult{}, err
	}
	if failure := response.GetFailure(); failure != nil {
		return StopResult{}, rpcFailure(failure)
	}
	return StopResult{Generation: response.GetGeneration(), Sequence: response.GetSequence()}, nil
}

func (c *GRPCClient) Snapshot(ctx context.Context) (Snapshot, error) {
	response, err := c.client.Snapshot(ctx, &grpcproto.SessionSnapshotRequest{SessionId: c.session()})
	if err != nil {
		return Snapshot{}, err
	}
	if failure := response.GetFailure(); failure != nil {
		return Snapshot{}, rpcFailure(failure)
	}
	value := response.GetSnapshot()
	if value == nil {
		return Snapshot{}, fmt.Errorf("session snapshot response was empty")
	}
	c.remember(value)
	return snapshot(value), nil
}

func (c *GRPCClient) Watch(ctx context.Context) (<-chan Snapshot, error) {
	stream, err := c.client.Watch(ctx, &grpcproto.SessionSnapshotRequest{SessionId: c.session()})
	if err != nil {
		return nil, err
	}
	updates := make(chan Snapshot, 1)
	go func() {
		defer func() {
			// A service restart invalidates the old opaque session owner.  The
			// next Snapshot with an empty ID bootstraps the replacement session;
			// retaining the predecessor ID would leave the UI stuck reconnecting.
			c.clearSession()
			close(updates)
		}()
		for {
			value, recvErr := stream.Recv()
			if recvErr != nil {
				return
			}
			c.remember(value)
			select {
			case updates <- snapshot(value):
			case <-ctx.Done():
				return
			default:
				// Snapshot streams are deliberately coalesced.  The next read is
				// authoritative, so a slow UI must not block the service stream.
				select {
				case <-updates:
				default:
				}
				select {
				case updates <- snapshot(value):
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return updates, nil
}

func (c *GRPCClient) Reset(ctx context.Context, sequence uint64) (Snapshot, error) {
	response, err := c.client.Reset(ctx, &grpcproto.SessionResetRequest{
		SessionId:        c.session(),
		ExpectedSequence: sequence,
	})
	if err != nil {
		return Snapshot{}, err
	}
	if failure := response.GetFailure(); failure != nil {
		return Snapshot{}, rpcFailure(failure)
	}
	value := response.GetSnapshot()
	if value == nil {
		return Snapshot{}, fmt.Errorf("reset response did not include a snapshot")
	}
	c.remember(value)
	return snapshot(value), nil
}

func rpcFailure(value *grpcproto.SessionFailure) error {
	return fmt.Errorf("%s: %s", failureName(value.GetCode()), value.GetMessage())
}

func snapshot(value *grpcproto.SessionSnapshot) Snapshot {
	result := Snapshot{
		SessionID:   value.GetSessionId(),
		Sequence:    value.GetSequence(),
		Generation:  value.GetGeneration(),
		State:       State(stateName(value.GetState())),
		Configured:  value.GetConfigured(),
		Digest:      value.GetDigest(),
		SourceKind:  sourceKind(value.GetSourceKind()),
		Profiles:    profiles(value.GetProfiles()),
		Warnings:    warnings(value.GetWarnings()),
		CleanupDone: value.GetCleanupComplete(),
		Recovering:  value.GetRecovering(),
	}
	if profile := value.GetActiveProfile(); profile != nil {
		converted := profileValue(profile)
		result.ActiveProfile = &converted
	}
	if failure := value.GetLastFailure(); failure != nil {
		result.LastFailure = &Failure{Code: failureName(failure.GetCode()), Message: failure.GetMessage()}
	}
	return result
}

func profiles(values []*grpcproto.SessionProfile) []Profile {
	result := make([]Profile, 0, len(values))
	for _, value := range values {
		if value != nil {
			result = append(result, profileValue(value))
		}
	}
	return result
}

func profileValue(value *grpcproto.SessionProfile) Profile {
	return Profile{Index: value.GetIndex(), Protocol: Protocol(protocolName(value.GetProtocol())), Description: value.GetDescription()}
}

func warnings(values []*grpcproto.SessionWarning) []Warning {
	result := make([]Warning, 0, len(values))
	for _, value := range values {
		if value != nil {
			result = append(result, Warning{Code: value.GetCode(), Message: value.GetMessage()})
		}
	}
	return result
}

func stateName(value grpcproto.SessionState) string {
	return strings.TrimPrefix(value.String(), "SESSION_STATE_")
}

func protocolName(value grpcproto.SessionProtocol) string {
	return strings.TrimPrefix(value.String(), "SESSION_PROTOCOL_")
}

func failureName(value grpcproto.SessionFailureCode) string {
	return strings.TrimPrefix(value.String(), "SESSION_FAILURE_CODE_")
}

func sourceKind(value grpcproto.SessionSourceKind) string {
	return strings.TrimPrefix(value.String(), "SESSION_SOURCE_KIND_")
}
