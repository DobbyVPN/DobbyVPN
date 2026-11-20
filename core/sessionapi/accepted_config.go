package sessionapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// AcceptedStore holds one protected, atomically replaced record. URL-only stores
// continue to work and can acquire a record on their next successful fetch.
type AcceptedStore interface {
	LoadAccepted(context.Context) ([]byte, error)
	SaveAccepted(context.Context, []byte) error
	ClearAccepted(context.Context) error
}

type acceptedConfiguration struct {
	Version    int       `json:"version"`
	Source     string    `json:"source"`
	Body       []byte    `json:"body"`
	Digest     string    `json:"digest"`
	AcceptedAt time.Time `json:"accepted_at"`
}

// Covers a base64 body and a worst-case JSON-escaped source, each individually bounded.
const maxAcceptedRecord = 8 * maxConfigBytes

func readAccepted(ctx context.Context, store AcceptedStore) (*acceptedConfiguration, error) {
	raw, err := store.LoadAccepted(ctx)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	var record acceptedConfiguration
	if len(raw) > maxAcceptedRecord || json.Unmarshal(raw, &record) != nil || record.Version != 1 ||
		record.AcceptedAt.IsZero() || len(record.Body) == 0 || len(record.Body) > maxConfigBytes ||
		len(record.Source) > maxConfigBytes {
		return nil, errors.New("invalid accepted configuration record")
	}
	u, err := url.Parse(record.Source)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("invalid accepted configuration source")
	}
	digest := sha256.Sum256(record.Body)
	if hex.EncodeToString(digest[:]) != record.Digest {
		return nil, errors.New("accepted configuration digest mismatch")
	}
	return &record, nil
}

func storedSource(ctx context.Context, store SourceStore) ([]byte, error) {
	if cache, ok := store.(AcceptedStore); ok {
		record, err := readAccepted(ctx, cache)
		if err != nil {
			return nil, fmt.Errorf("read accepted configuration: %w", err)
		}
		if record != nil {
			return []byte(record.Source), nil
		}
	}
	return store.Load(ctx)
}

func (m *Manager) loadForRestore(ctx context.Context, store AcceptedStore, source []byte, original error) (LoadedConfig, parsedConfig, error) {
	record, err := readAccepted(ctx, store)
	if err != nil {
		return LoadedConfig{}, parsedConfig{}, failureWithCause(FailurePlatform, "cached configuration could not be read", err)
	}
	if record == nil || record.Source != string(source) {
		return LoadedConfig{}, parsedConfig{}, original
	}
	parsed, err := parseConfig(record.Body)
	if err != nil {
		return LoadedConfig{}, parsedConfig{}, failureWithCause(FailureMalformedConfig, "cached configuration is invalid", err)
	}
	return LoadedConfig{Kind: ConfigSourceURL, SourceURL: record.Source, Raw: record.Body, OfflineAcceptedAt: record.AcceptedAt.UTC().Format(time.RFC3339Nano)}, parsed, nil
}
