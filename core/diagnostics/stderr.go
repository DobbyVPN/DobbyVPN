package diagnostics

import (
	"core/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var stderrMu sync.Mutex
var stderrCapture *rawCapture

type rawCapture struct {
	mu         sync.Mutex
	path       string
	file       *os.File
	limit      int64
	run        string
	generation uint64
}

// Raw descriptors have no whole-record framing. Preserve their file identities;
// the capture monitor owns rename rotation rather than structured migration.
func openRawCapture(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	var file *os.File
	err := withFileLock(path, func() error {
		for _, name := range []string{path + PreviousSuffix, path} {
			info, err := os.Stat(name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return &os.PathError{Op: "open raw capture", Path: name, Err: os.ErrInvalid}
			}
		}
		opened, err := openAppend(path)
		if err != nil {
			return err
		}
		if _, err := regularFile(opened); err != nil {
			return errors.Join(err, opened.Close())
		}
		file = opened
		return nil
	})
	if err != nil {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
		return nil, err
	}
	return file, nil
}

// CaptureStderr redirects the OS descriptor to a regular append-only file.
// Panic/startup output never depends on draining a pipe or a Go log callback.
// The monitor replaces descriptors; an in-flight write keeps the old inode,
// which remains the previous generation and may exceed the rotation threshold.
func CaptureStderr(path, permissionsFrom string) error {
	stderrMu.Lock()
	defer stderrMu.Unlock()
	if stderrCapture != nil {
		if stderrCapture.path != path {
			return fmt.Errorf("stderr already captured at %s", stderrCapture.path)
		}
		return nil
	}
	file, err := openRawCapture(path)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(permissionsFrom); statErr == nil {
		if modeErr := preservePermissions(file, info); modeErr != nil {
			return errors.Join(modeErr, file.Close())
		}
	}
	capture := &rawCapture{path: path, file: file, limit: Threshold, run: fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())}
	if err := capture.writeHeader(file); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := redirectStderr(file); err != nil {
		return errors.Join(err, file.Close())
	}
	stderrCapture = capture
	go capture.monitor()
	return nil
}

func (capture *rawCapture) writeHeader(file *os.File) error {
	capture.generation++
	metadata, err := json.Marshal(map[string]any{
		"event": "stderr.capture", "timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"process_id": os.Getpid(), "run_id": capture.run, "capture_generation": capture.generation,
		"build": buildinfo.Fields(),
	})
	if err != nil {
		return err
	}
	_, err = file.Write(append(metadata, '\n'))
	return err
}

func (capture *rawCapture) monitor() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		if err := capture.rotate(); err != nil {
			// Direct stderr only: never recurse through the structured sink.
			_, _ = fmt.Fprintf(Stderr, "stderr rotation failed; rotation stopped: %v\n", err)
			return
		}
	}
}

func (capture *rawCapture) rotate() error {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return withFileLock(capture.path, func() error {
		info, err := capture.file.Stat()
		if err != nil {
			return err
		}
		if info.Size() < capture.limit {
			return nil
		}
		if rotateErr := rotateFiles(capture.path); rotateErr != nil {
			return rotateErr
		}
		next, err := openAppend(capture.path)
		if err != nil {
			return err
		}
		if err := preservePermissions(next, info); err != nil {
			return errors.Join(err, next.Close())
		}
		if err := capture.writeHeader(next); err != nil {
			return errors.Join(err, next.Close())
		}
		if err := redirectStderr(next); err != nil {
			return errors.Join(err, next.Close())
		}
		previous := capture.file
		capture.file = next
		return previous.Close()
	})
}

// Stderr follows the capture under its descriptor lock. It also works before
// initialization and reports logger failures without calling the shared logger.
var Stderr io.Writer = stderrWriter{}

type stderrWriter struct{}

func (stderrWriter) Write(data []byte) (int, error) {
	stderrMu.Lock()
	capture := stderrCapture
	stderrMu.Unlock()
	if capture == nil {
		return os.Stderr.Write(data)
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.file.Write(data)
}
