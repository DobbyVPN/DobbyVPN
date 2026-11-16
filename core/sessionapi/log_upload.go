package sessionapi

import (
	"context"
	"encoding/base64"
	"net/url"

	"github.com/google/uuid"
)

const LogUploadMaxBytes int64 = 150000000

type LogUploadPreparation struct {
	Reference     string `json:"reference"`
	Destination   string `json:"destination"`
	Authorization string `json:"authorization"`
	Provider      string `json:"provider"`
	MaxBytes      int64  `json:"max_bytes"`
}

// PrepareLogUpload fences the accepted inventory and returns immutable upload
// parameters. The caller owns archive creation and the network transfer.
func (m *Manager) PrepareLogUpload(_ context.Context, sessionID, digest string) (LogUploadPreparation, error) {
	if sessionID == "" || digest == "" {
		return LogUploadPreparation{}, failure(FailureInvalidArgument, "session ID and configuration digest are required")
	}
	s, err := m.get(sessionID)
	if err != nil {
		return LogUploadPreparation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if digest != s.digest {
		return LogUploadPreparation{}, failure(FailureConflict, "configuration changed; refresh its snapshot")
	}
	if s.logsEndpoint == "" {
		return LogUploadPreparation{}, failure(FailureConflict, "log upload is unavailable")
	}
	endpoint, err := url.Parse(s.logsEndpoint)
	if err != nil {
		return LogUploadPreparation{}, failure(FailureInternal, "accepted log endpoint is invalid")
	}
	authorization := ""
	if endpoint.User != nil {
		password, _ := endpoint.User.Password()
		authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte(endpoint.User.Username()+":"+password))
		endpoint.User = nil
	}
	provider := endpoint.String()
	id, err := uuid.NewV7()
	if err != nil {
		return LogUploadPreparation{}, failureWithCause(FailureInternal, "could not allocate upload reference", err)
	}
	reference := id.String()
	return LogUploadPreparation{Reference: reference, Destination: provider + reference + ".gz", Authorization: authorization, Provider: provider, MaxBytes: LogUploadMaxBytes}, nil
}
