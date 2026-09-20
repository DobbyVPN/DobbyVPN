//go:build android || ios

package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	applicationlog "go_module/log"
)

// NewMobileClient constructs the shared client around the platform's narrow
// native transport. The UI never owns platform session or sharing policy.
func NewMobileClient() *MobileClient {
	return NewMobileClientWithTransport(newMobileAPI())
}

// NewMobileLogExporter returns the platform's existing explicit share adapter
// when one is available. A nil result leaves the shared action disabled rather
// than making the UI invent a second native sharing implementation.
func NewMobileLogExporter() LogExporter { return newMobileLogExporter() }

// NewMobileDiagnosticStore resolves the app-owned mobile files through the
// native shell and injects the same FileDiagnosticStore used by desktop. The
// Go UI never accepts a path from a caller and never guesses HOME or an iOS
// App Group location. A bridge failure still returns a non-nil store so the
// production constructor can report unavailable diagnostics explicitly.
func NewMobileDiagnosticStore() DiagnosticStore {
	paths, err := platformDiagnosticPaths()
	if err == nil {
		return newMobileDiagnosticStore(paths)
	}
	return unavailableMobileDiagnosticStore{err: err}
}

func newMobileDiagnosticStore(paths []string) DiagnosticStore {
	store, err := mobileFileDiagnosticStore(paths)
	if err != nil {
		return unavailableMobileDiagnosticStore{err: err}
	}
	return store
}

// mobileFileDiagnosticStore validates the native path contract before handing
// paths to the shared store. All files must be in one native-owned directory;
// only the native bridge can supply that directory. The marker is the only
// file the UI clears, while every producer file remains append-only.
func mobileFileDiagnosticStore(paths []string) (DiagnosticStore, error) {
	if len(paths) < 3 {
		return nil, errors.New("mobile diagnostic bridge returned too few paths")
	}
	cleaned := make([]string, len(paths))
	root := ""
	seen := make(map[string]struct{}, len(paths))
	goPath := ""
	allowedProducerNames := map[string]struct{}{
		"app_logs.txt":         {},
		"native_logs.jsonl":    {},
		"go_app_logs.jsonl":    {},
		"go_tunnel_logs.jsonl": {},
	}
	for index, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" || !filepath.IsAbs(value) {
			return nil, fmt.Errorf("mobile diagnostic path %d is not absolute", index)
		}
		clean := filepath.Clean(value)
		if clean != value || clean == string(filepath.Separator) {
			return nil, fmt.Errorf("mobile diagnostic path %d is not canonical", index)
		}
		if _, ok := seen[clean]; ok {
			return nil, errors.New("mobile diagnostic bridge returned duplicate paths")
		}
		seen[clean] = struct{}{}
		if index == 0 {
			root = filepath.Dir(clean)
			if filepath.Base(clean) != "ui_diagnostics.jsonl" {
				return nil, errors.New("mobile diagnostic marker has an unexpected name")
			}
		} else if filepath.Dir(clean) != root {
			return nil, errors.New("mobile diagnostic paths escape the native app directory")
		} else if _, ok := allowedProducerNames[filepath.Base(clean)]; !ok {
			return nil, fmt.Errorf("mobile diagnostic producer %q is not an app-owned file", filepath.Base(clean))
		}
		if filepath.Base(clean) == "go_app_logs.jsonl" {
			goPath = clean
		}
		cleaned[index] = clean
	}
	if goPath == "" {
		return nil, errors.New("mobile diagnostic bridge did not provide the Go app log path")
	}
	if err := applicationlog.SetPath(goPath); err != nil {
		return nil, fmt.Errorf("initialize mobile Go diagnostic logger: %w", err)
	}
	return newFileDiagnosticStore(cleaned[0], cleaned[1:]...), nil
}

type unavailableMobileDiagnosticStore struct{ err error }

func (s unavailableMobileDiagnosticStore) Read(ctx context.Context) (DiagnosticHistory, error) {
	if err := ctx.Err(); err != nil {
		return DiagnosticHistory{}, err
	}
	if s.err == nil {
		return DiagnosticHistory{}, errors.New("mobile diagnostic bridge unavailable")
	}
	return DiagnosticHistory{}, fmt.Errorf("mobile diagnostic bridge unavailable: %w", s.err)
}

func (s unavailableMobileDiagnosticStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err == nil {
		return errors.New("mobile diagnostic bridge unavailable")
	}
	return fmt.Errorf("mobile diagnostic bridge unavailable: %w", s.err)
}
