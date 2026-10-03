package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"
)

// Set from the repository VERSION by the desktop builder.
var appVersion = "development"

func diagnosticPaths() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	backend := os.Getenv("DOBBY_LOG_PATH")
	if backend == "" {
		switch runtime.GOOS {
		case "windows":
			backend = filepath.Join(os.Getenv("ProgramData"), "DobbyVPN", "Logs", "backend.jsonl")
		case "darwin":
			backend = "/Library/Logs/DobbyVPN/backend.jsonl"
		default:
			backend = "/var/log/dobbyvpn/backend.jsonl"
		}
	}
	paths := []string{backend}
	if base, configErr := os.UserConfigDir(); configErr == nil && runtime.GOOS != "windows" {
		local := filepath.Join(base, "DobbyVPN", "Logs", "backend.jsonl")
		if local != backend {
			paths = append(paths, local)
		}
	}
	cli := os.Getenv("DOBBY_CLI_LOG_PATH")
	if cli == "" {
		cli = applicationLogPath(home)
	}
	return append(paths, cli), nil
}

func runLogs(args []string) int {
	if len(args) == 1 && args[0] == "clear" {
		return clearApplicationLog()
	}
	follow := len(args) == 1 && args[0] == "--follow"
	export := len(args) == 2 && args[0] == "export"
	if len(args) > 0 && !follow && !export {
		return usage("logs accepts --follow, export <new-file>, or clear")
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

func exportDiagnostics(destination string, paths []string) (err error) {
	// Never overwrite a diagnostic input or an existing user file.
	// #nosec G703 -- The CLI operator explicitly selects this new export path; O_EXCL refuses existing files and symlinks.
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return streamDiagnostics(context.Background(), file, paths, false)
}

type logCursor struct {
	info   os.FileInfo
	offset int64
}

func (cursor *logCursor) copyAvailable(output io.Writer, path string) (err error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("diagnostic path is not a regular file: %s", path)
	}
	if cursor.info == nil || !os.SameFile(cursor.info, info) || info.Size() < cursor.offset {
		cursor.offset = 0
	}
	cursor.info = info
	if info.Size() == cursor.offset {
		return nil
	}
	if _, writeErr := fmt.Fprintf(output, "\n--- %s ---\n", path); writeErr != nil {
		return writeErr
	}
	if _, seekErr := file.Seek(cursor.offset, io.SeekStart); seekErr != nil {
		return seekErr
	}
	count, err := io.CopyN(output, file, info.Size()-cursor.offset)
	cursor.offset += count
	return err
}

func streamDiagnostics(ctx context.Context, output io.Writer, paths []string, follow bool) error {
	if _, err := fmt.Fprintf(output, "DobbyVPN %s\nPlatform: %s/%s\nCaptured: %s\n", appVersion, runtime.GOOS, runtime.GOARCH, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	cursors := make([]logCursor, len(paths))
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var failures []error
		for index, path := range paths {
			if err := cursors[index].copyAvailable(output, path); err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) > 0 {
			return errors.Join(failures...)
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
