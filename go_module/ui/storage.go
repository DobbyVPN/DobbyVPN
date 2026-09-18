package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const maxStoredSource = 1 << 20

// SourceStore owns the one piece of user configuration that the UI is allowed
// to persist.  Session state, selected profiles and protocol caches remain
// service-owned and are never written by the UI.
type SourceStore interface {
	Load(context.Context) ([]byte, error)
	Save(context.Context, []byte) error
}

// FileSourceStore is the desktop store.  Its path deliberately matches the
// storage used by the 1.5.0 desktop application so an upgrade keeps the
// accepted source without keeping the JVM runtime.
type FileSourceStore struct {
	path   string
	legacy string
}

func NewFileSourceStore() (*FileSourceStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find user home for connection source: %w", err)
	}
	return &FileSourceStore{
		path:   filepath.Join(home, ".dobbyvpn", "configs", "connection-url.txt"),
		legacy: filepath.Join(home, ".myapp", "configs", "connection-url.txt"),
	}, nil
}

func (s *FileSourceStore) Load(ctx context.Context) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	path := s.path
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		path = s.legacy
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read connection source: %w", err)
	}
	if len(raw) > maxStoredSource {
		return nil, fmt.Errorf("stored connection source exceeds %d bytes", maxStoredSource)
	}
	return raw, nil
}

func (s *FileSourceStore) Save(ctx context.Context, raw []byte) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if len(raw) > maxStoredSource {
		return fmt.Errorf("connection source exceeds %d bytes", maxStoredSource)
	}
	if len(raw) == 0 {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove connection source: %w", err)
		}
		return nil
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create connection source directory: %w", err)
	}
	_ = os.Chmod(dir, 0o700)
	temporary, err := os.CreateTemp(dir, ".dobby-source-*.tmp")
	if err != nil {
		return fmt.Errorf("create connection source temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict connection source temporary file: %w", err)
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write connection source: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close connection source temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		// Windows does not replace an existing destination with Rename. Remove
		// only this known file, then retry; the source remains in the temp file
		// until this operation succeeds or the deferred cleanup runs.
		if removeErr := os.Remove(s.path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("replace connection source: %w", err)
		}
		if retryErr := os.Rename(temporaryPath, s.path); retryErr != nil {
			return fmt.Errorf("replace connection source: %w", retryErr)
		}
	}
	_ = os.Chmod(s.path, 0o600)
	return nil
}

// MemorySourceStore is useful for UI tests and mobile adapters that provide
// secure storage in their native shell.
type MemorySourceStore struct {
	mu    sync.RWMutex
	value []byte
}

func (s *MemorySourceStore) Load(ctx context.Context) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]byte(nil), s.value...), nil
}

func (s *MemorySourceStore) Save(ctx context.Context, raw []byte) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if len(raw) > maxStoredSource {
		return fmt.Errorf("connection source exceeds %d bytes", maxStoredSource)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = append(s.value[:0], raw...)
	return nil
}
