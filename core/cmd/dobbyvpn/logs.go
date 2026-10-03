package main

import (
	"context"
	"core/buildinfo"
	"core/diagnostics"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

const windowsPlatform = "windows"

func cliLogPath() (string, error) {
	if path := os.Getenv("DOBBY_CLI_LOG_PATH"); path != "" {
		return filepath.Abs(path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return applicationLogPath(home), nil
}

func diagnosticPaths() ([]string, error) {
	backend := os.Getenv("DOBBY_LOG_PATH")
	if backend == "" {
		switch runtime.GOOS {
		case windowsPlatform:
			backend = filepath.Join(os.Getenv("ProgramData"), "DobbyVPN", "Logs", "backend.jsonl")
		case "darwin":
			backend = "/Library/Logs/DobbyVPN/backend.jsonl"
		default:
			backend = "/var/log/dobbyvpn/backend.jsonl"
		}
	}
	paths := []string{backend, backend + ".stderr"}
	if base, err := os.UserConfigDir(); err == nil && runtime.GOOS != windowsPlatform {
		local := filepath.Join(base, "DobbyVPN", "Logs", "backend.jsonl")
		if local != backend {
			paths = append(paths, local, local+".stderr")
		}
	}
	cli, err := cliLogPath()
	if err != nil {
		return nil, err
	}
	paths = append(paths, cli, cli+".stderr")
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "darwin":
			paths = append(paths, filepath.Join(home, "Library", "Logs", "DobbyVPN", "ui_diagnostics.jsonl"))
		case windowsPlatform:
			paths = append(paths, filepath.Join(os.Getenv("LOCALAPPDATA"), "DobbyVPN", "Logs", "ui_diagnostics.jsonl"))
		}
	}
	return paths, nil
}

func runLogs(args []string) int {
	if len(args) == 1 && args[0] == "clear" {
		return clearApplicationLog()
	}
	follow := len(args) == 1 && args[0] == "--follow"
	export := len(args) == 2 && args[0] == "export"
	if len(args) > 0 && !follow && !export {
		return usage("logs accepts --follow, export <new-file.gz>, or clear")
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
