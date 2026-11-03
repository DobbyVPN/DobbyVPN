package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"core/buildinfo"
	"core/clientserver/controljson"
	"core/diagnostics"
	"core/sessionapi"

	"github.com/google/uuid"
)

const windowsPlatform = "windows"
const darwinPlatform = "darwin"

func cliLogPath() (string, error) {
	if path := os.Getenv("DOBBY_CLI_LOG_PATH"); path != "" {
		return filepath.Abs(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case darwinPlatform:
		return filepath.Join(home, "Library", "Logs", "DobbyVPN", "ui_diagnostics.jsonl"), nil
	case windowsPlatform:
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "DobbyVPN", "Logs", "ui_diagnostics.jsonl"), nil
	default:
		return applicationLogPath(home), nil
	}
}

func diagnosticPaths() ([]string, error) {
	backend := os.Getenv("DOBBY_LOG_PATH")
	if backend == "" {
		switch runtime.GOOS {
		case windowsPlatform:
			backend = filepath.Join(os.Getenv("ProgramData"), "DobbyVPN", "Logs", "backend.jsonl")
		case darwinPlatform:
			backend = "/Library/Logs/DobbyVPN/backend.jsonl"
		default:
			backend = "/var/log/dobbyvpn/backend.jsonl"
		}
	}
	paths := make([]string, 0, 2)
	paths = append(paths, backend)
	cli, err := cliLogPath()
	if err != nil {
		return nil, err
	}
	paths = append(paths, cli)
	return paths, nil
}

func runLogs(args []string) int {
	if len(args) == 1 && args[0] == "clear" {
		return clearApplicationLog()
	}
	if len(args) == 1 && args[0] == "upload" {
		paths, err := diagnosticPaths()
		if err != nil {
			return reportFailure(err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := uploadDiagnostics(ctx, dialService(), paths); err != nil {
			return reportFailure(err)
		}
		return exitOK
	}
	follow := len(args) == 1 && args[0] == "--follow"
	export := len(args) == 2 && args[0] == "export"
	if len(args) > 0 && !follow && !export {
		return usage("logs accepts --follow, export <new-file.gz>, upload, or clear")
	}
	paths, err := diagnosticPaths()
	if err != nil {
		return reportFailure(err)
	}
	if export {
		if err := exportDiagnostics(args[1], paths); err != nil {
			return reportFailure(err)
		}
		fmt.Println(args[1])
		return exitOK
	}
	if path, err := cliLogPath(); err == nil {
		if data, err := os.ReadFile(path + ".upload"); err == nil {
			var saved struct {
				Reference string `json:"reference"`
			}
			if json.Unmarshal(data, &saved) == nil && saved.Reference != "" {
				fmt.Printf("Latest uploaded logs: %s\n", saved.Reference)
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := streamDiagnostics(ctx, os.Stdout, paths, follow); err != nil {
		return reportFailure(err)
	}
	return exitOK
}

func diagnosticHeader() string {
	metadata, _ := json.Marshal(buildinfo.Fields())
	return fmt.Sprintf("DobbyVPN diagnostics\nBuild: %s\nCaptured: %s\n", metadata, time.Now().UTC().Format(time.RFC3339Nano))
}

func exportDiagnostics(destination string, paths []string) error {
	return diagnostics.ExportGzip(destination, paths, diagnosticHeader())
}

// Clear stores stable file identities and lengths; it never changes producer
// bytes. A later process and an existing follower both observe this boundary.
func saveViewBoundary(paths []string) (resultErr error) {
	snapshot, err := diagnostics.Capture(paths)
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	if err != nil {
		return err
	}
	offsets := make(map[string]int64, len(snapshot.Inputs))
	for _, input := range snapshot.Inputs {
		offsets[input.ID] = input.Size
	}
	path, err := cliLogPath()
	if err != nil {
		return err
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o700); mkdirErr != nil {
		return mkdirErr
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".view-*")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, removeViewTemp(file.Name())) }()
	writeErr := json.NewEncoder(file).Encode(offsets)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path+".view")
}

func removeViewTemp(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func readViewBoundary() (offsets map[string]int64, resultErr error) {
	path, err := cliLogPath()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path + ".view")
	if errors.Is(err, os.ErrNotExist) {
		return map[string]int64{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	err = json.NewDecoder(io.LimitReader(file, 64*1024)).Decode(&offsets)
	return offsets, err
}

type logView struct {
	offsets  map[string]int64
	retained *diagnostics.Snapshot
}

func (view *logView) close() error {
	if view.retained == nil {
		return nil
	}
	return view.retained.Close()
}

func (view *logView) copyAvailable(output io.Writer, paths []string, boundary map[string]int64) error {
	next, captureErr := diagnostics.Capture(paths)
	failures := []error{captureErr}
	if view.offsets == nil {
		view.offsets = map[string]int64{}
	}
	// Drain handles retained from the last poll even if their names were removed
	// by rotation. Refresh their sizes to include writes since that poll.
	if view.retained != nil {
		for _, input := range view.retained.Inputs {
			info, err := input.File.Stat()
			if err != nil {
				failures = append(failures, err)
				continue
			}
			input.Size = info.Size()
			failures = append(failures, view.copyInput(output, input, boundary))
		}
		failures = append(failures, view.retained.Close())
	}
	live := make(map[string]int64, len(next.Inputs))
	for _, input := range next.Inputs {
		failures = append(failures, view.copyInput(output, input, boundary))
		live[input.ID] = view.offsets[input.ID]
	}
	view.offsets, view.retained = live, next
	return errors.Join(failures...)
}

func (view *logView) copyInput(output io.Writer, input diagnostics.Input, boundary map[string]int64) error {
	offset := max(view.offsets[input.ID], boundary[input.ID])
	if offset >= input.Size {
		view.offsets[input.ID] = offset
		return nil
	}
	if _, err := fmt.Fprintf(output, "\n--- %s ---\n", input.Path); err != nil {
		return err
	}
	count, err := input.CopyTo(output, offset)
	view.offsets[input.ID] = offset + count
	return err
}

func streamDiagnostics(ctx context.Context, output io.Writer, paths []string, follow bool) (resultErr error) {
	if _, err := io.WriteString(output, diagnosticHeader()); err != nil {
		return err
	}
	view := &logView{}
	defer func() { resultErr = errors.Join(resultErr, view.close()) }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		boundary, err := readViewBoundary()
		if err != nil {
			return fmt.Errorf("read log view boundary: %w", err)
		}
		if err := view.copyAvailable(output, paths, boundary); err != nil {
			return err
		}
		if !follow {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// uploadDiagnostics creates a fresh archive for each manual attempt and keeps
// upload credentials out of persistent preferences and diagnostic messages.
func uploadDiagnostics(ctx context.Context, client controljson.Client, paths []string) error {
	return uploadDiagnosticsWithHTTP(ctx, client, paths, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}

func uploadDiagnosticsWithHTTP(ctx context.Context, client controljson.Client, paths []string, transport *http.Client) (resultErr error) {
	snapshot, snapshotErr := callSnapshot(ctx, client, "")
	if snapshotErr != nil {
		return snapshotErr
	}
	var prepared sessionapi.LogUploadPreparation
	if err := client.Call(ctx, "PrepareLogUpload", struct {
		SessionID string `json:"session_id"`
		Digest    string `json:"digest"`
	}{snapshot.SessionID, snapshot.Digest}, &prepared); err != nil {
		return err
	}
	logPath, pathErr := cliLogPath()
	if pathErr != nil {
		return pathErr
	}
	tempDir := filepath.Join(filepath.Dir(logPath), "upload-temp")
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return err
	}
	lock, lockErr := os.OpenFile(filepath.Join(tempDir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if lockErr != nil {
		return lockErr
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err := tryUploadLock(lock); err != nil {
		if uploadLockContended(err) {
			return fmt.Errorf("another log upload is in progress: %w", err)
		}
		return fmt.Errorf("lock upload directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, releaseUploadLock(lock)) }()
	if err := cleanupUploadTemps(tempDir); err != nil {
		return err
	}
	// The UUID comes from the fenced preparation, and is the remote filename.
	id, parseErr := uuid.Parse(prepared.Reference)
	if parseErr != nil || id.Version() != 7 || id.String() != prepared.Reference {
		return errors.New("invalid upload reference")
	}
	name := filepath.Join(tempDir, prepared.Reference+".gz")
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := diagnostics.ExportGzipContext(ctx, name, paths, diagnosticHeader()); err != nil {
		return fmt.Errorf("create diagnostics archive: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, removeViewTemp(name)) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return sendLogArchive(ctx, transport, name, prepared, func() error {
		fmt.Println(prepared.Reference)
		return saveUploadPreference(prepared)
	})
}

func sendLogArchive(ctx context.Context, transport *http.Client, name string, prepared sessionapi.LogUploadPreparation, onCreated func() error) (resultErr error) {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() > prepared.MaxBytes {
		return fmt.Errorf("diagnostics archive exceeds upload size limit")
	}
	transferCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	request, err := http.NewRequestWithContext(transferCtx, http.MethodPut, prepared.Destination, io.NopCloser(file))
	if err != nil {
		return errors.New("invalid upload destination")
	}
	request.ContentLength = info.Size()
	request.Header.Set("Content-Type", "application/gzip")
	request.Header.Set("If-None-Match", "*")
	if prepared.Authorization != "" {
		request.Header.Set("Authorization", prepared.Authorization)
	}
	response, err := transport.Do(request)
	if err != nil {
		return fmt.Errorf("log upload failed: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, response.Body.Close()) }()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("log upload returned HTTP %d", response.StatusCode)
	}
	return onCreated()
}

func saveUploadPreference(prepared sessionapi.LogUploadPreparation) (resultErr error) {
	path, pathErr := cliLogPath()
	if pathErr != nil {
		return pathErr
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, tempErr := os.CreateTemp(filepath.Dir(path), ".upload-preference-*")
	if tempErr != nil {
		return tempErr
	}
	defer func() { resultErr = errors.Join(resultErr, removeViewTemp(temp.Name())) }()
	metadata := struct {
		Reference string    `json:"reference"`
		Time      time.Time `json:"time"`
		Provider  string    `json:"provider"`
	}{prepared.Reference, time.Now().UTC(), prepared.Provider}
	writeErr := json.NewEncoder(temp).Encode(metadata)
	closeErr := temp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if err := replaceUploadPreference(temp.Name(), path+".upload"); err != nil {
		return err
	}
	return nil
}

// Cleanup is called only while the process holds the cross-process upload lock.
func cleanupUploadTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".gz") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".gz")
		id, err := uuid.Parse(name)
		if err != nil || id.Version() != 7 || id.String() != name {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func cleanupAbandonedUploads() (resultErr error) {
	path, pathErr := cliLogPath()
	if pathErr != nil {
		return pathErr
	}
	dir := filepath.Join(filepath.Dir(path), "upload-temp")
	if mkdirErr := os.MkdirAll(dir, 0o700); mkdirErr != nil {
		return mkdirErr
	}
	lock, openErr := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if openErr != nil {
		return openErr
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err := tryUploadLock(lock); err != nil {
		if uploadLockContended(err) {
			return nil
		}
		return fmt.Errorf("lock upload directory for cleanup: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, releaseUploadLock(lock)) }()
	return cleanupUploadTemps(dir)
}
