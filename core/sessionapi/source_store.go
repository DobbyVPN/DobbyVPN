package sessionapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxStoredSourceURL = 1 << 20

// SourceStore persists only the accepted source URL. Configuration bytes and
// parsed profile data remain owned by the current session.
type SourceStore interface {
	Load(context.Context) ([]byte, error)
	Save(context.Context, []byte) error
	Clear(context.Context) error
}

type FileSourceStore struct {
	Path   string
	Legacy string
}

func (s FileSourceStore) Load(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := s.read(s.Path)
	if err == nil {
		return raw, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if s.Legacy == "" {
		return nil, nil
	}
	legacy, legacyErr := s.read(s.Legacy)
	if legacyErr != nil {
		if errors.Is(legacyErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, legacyErr
	}
	if err := s.Save(ctx, legacy); err != nil {
		return legacy, fmt.Errorf("migrate saved configuration URL: %w", err)
	}
	if err := os.Remove(s.Legacy); err != nil && !errors.Is(err, os.ErrNotExist) {
		return legacy, fmt.Errorf("remove migrated configuration URL: %w", err)
	}
	return legacy, nil
}

func (s FileSourceStore) Save(ctx context.Context, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > maxStoredSourceURL {
		return fmt.Errorf("saved configuration URL must contain 1 to %d bytes", maxStoredSourceURL)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return errors.New("saved configuration URL is empty")
	}
	dir := filepath.Dir(s.Path)
	if err := validateNoSymlinkAncestors(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create saved configuration directory: %w", err)
	}
	if err := validateSourcePath(dir, s.Path); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(dir, ".dobby-source-*.tmp")
	if err != nil {
		return fmt.Errorf("create saved configuration temporary file: %w", err)
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict saved configuration permissions: %w", err)
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write saved configuration URL: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("flush saved configuration URL: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close saved configuration URL: %w", err)
	}
	if err := replaceSourceFile(name, s.Path); err != nil {
		return fmt.Errorf("replace saved configuration URL: %w", err)
	}
	if err := os.Chmod(s.Path, 0o600); err != nil {
		return fmt.Errorf("restrict saved configuration permissions: %w", err)
	}
	return nil
}

func (s FileSourceStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, path := range []string{s.Path, s.Legacy} {
		if path == "" {
			continue
		}
		if err := validateSourcePath(filepath.Dir(path), path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove saved configuration URL: %w", err)
		}
	}
	return nil
}

func (s FileSourceStore) read(path string) ([]byte, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	if err := validateSourcePath(filepath.Dir(path), path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxStoredSourceURL {
		return nil, fmt.Errorf("saved configuration URL exceeds %d bytes", maxStoredSourceURL)
	}
	return raw, nil
}

func validateSourcePath(dir, path string) error {
	if err := validateNoSymlinkAncestors(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("saved configuration directory is not a real directory")
	}
	info, err = os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("saved configuration URL is not a regular file")
	}
	return nil
}

func validateNoSymlinkAncestors(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
			parent := filepath.Dir(current)
			if parent == current {
				return nil
			}
			current = parent
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("saved configuration path contains a symbolic link")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}
