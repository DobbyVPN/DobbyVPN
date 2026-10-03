// Package wire owns the JSON response contract for every Go control transport.
package wire

import "core/sessionapi"

// Response is the shared JSON envelope used by mobile, desktop and CLI clients.
type Response[T any] struct {
	OK     bool     `json:"ok"`
	Result T        `json:"result,omitempty"`
	Error  *Failure `json:"error,omitempty"`
}

type Profile struct {
	Index       int32  `json:"index"`
	Protocol    string `json:"protocol"`
	Description string `json:"description"`
}
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Configuration struct {
	Digest     string    `json:"digest"`
	Sequence   uint64    `json:"sequence,omitempty"`
	SourceKind string    `json:"source_kind"`
	Profiles   []Profile `json:"profiles"`
}
type Generation struct {
	Generation uint64 `json:"generation"`
	Sequence   uint64 `json:"sequence"`
}
type Snapshot struct {
	SessionID       string    `json:"session_id"`
	Sequence        uint64    `json:"sequence"`
	Generation      uint64    `json:"generation"`
	State           string    `json:"state"`
	Configured      bool      `json:"configured"`
	Digest          string    `json:"digest"`
	SourceKind      string    `json:"source_kind"`
	SourceURL       string    `json:"source_url,omitempty"`
	SourceError     string    `json:"source_error,omitempty"`
	Profiles        []Profile `json:"profiles"`
	ActiveProfile   *Profile  `json:"active_profile,omitempty"`
	LastFailure     *Failure  `json:"last_failure,omitempty"`
	CleanupComplete bool      `json:"cleanup_complete"`
	Recovering      bool      `json:"recovering"`
	PrimaryAction   string    `json:"primary_action"`
}

func profileResultDTO(in sessionapi.ProfileSummary) Profile {
	return Profile{Index: in.Index, Protocol: string(in.Protocol), Description: in.Description}
}
func profileResultPtr(in *sessionapi.ProfileSummary) *Profile {
	if in == nil {
		return nil
	}
	out := profileResultDTO(*in)
	return &out
}
func profilesDTO(in []sessionapi.ProfileSummary) []Profile {
	out := make([]Profile, len(in))
	for i := range in {
		out[i] = profileResultDTO(in[i])
	}
	return out
}
func ConfigurationFrom(in sessionapi.ConfigureResult) Configuration {
	return Configuration{
		Digest: in.Digest, Sequence: in.Sequence, SourceKind: string(in.SourceKind),
		Profiles: profilesDTO(in.Profiles),
	}
}
func SnapshotFrom(in sessionapi.SnapshotResult) Snapshot {
	out := Snapshot{
		SessionID: in.SessionID, Sequence: in.Sequence, Generation: in.Generation,
		State: string(in.State), Configured: in.Configured, Digest: in.Digest,
		SourceKind: string(in.SourceKind), SourceURL: in.SourceURL, SourceError: in.SourceError,
		Profiles:        profilesDTO(in.Profiles),
		ActiveProfile:   profileResultPtr(in.ActiveProfile),
		CleanupComplete: in.CleanupComplete,
		Recovering:      in.Recovering,
		PrimaryAction:   in.PrimaryAction,
	}
	if in.LastFailure != "" {
		out.LastFailure = &Failure{Code: string(in.LastFailure), Message: in.LastFailureMessage}
	}
	return out
}
