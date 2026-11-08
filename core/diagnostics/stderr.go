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
	mu                  sync.Mutex
	path                string
	file                *os.File
	limit               int64
	run                 string
	generation          uint64
	stdout              bool
	pending             bool // current generation was renamed; finish replacement before rotating again
	openReplacement     func(string) (*os.File, error)
	prepareReplacement  func(*os.File, os.FileInfo) error
	redirectReplacement func(*os.File) error
}

// Raw descriptors have no whole-record framing. Preserve their file identities;
// the capture monitor owns rename rotation rather than structured migration.
func openRawCapture(path string) (*os.File, error) {
	// #nosec G703 -- Raw log paths come from local logger configuration or app-private directories, not network input.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	var file *os.File
	err := withFileLock(path, func() error {
		for _, name := range []string{path + PreviousSuffix, path} {
			// #nosec G703 -- name is the configured local raw log path plus its fixed generation suffix.
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
	return captureOutput(path, permissionsFrom, false)
}

// CaptureOutput owns stdout and stderr together for the macOS daemon.
func CaptureOutput(path, permissionsFrom string) error {
	return captureOutput(path, permissionsFrom, true)
}

func captureOutput(path, permissionsFrom string, stdout bool) error {
	stderrMu.Lock()
	defer stderrMu.Unlock()
	if stderrCapture != nil {
		if stderrCapture.path != path || stderrCapture.stdout != stdout {
			return fmt.Errorf("process output already captured at %s", stderrCapture.path)
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
	capture := &rawCapture{path: path, file: file, limit: Threshold, run: fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano()), stdout: stdout}
	if err := capture.writeHeader(file); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := capture.redirect(file); err != nil {
		return errors.Join(err, file.Close())
	}
	stderrCapture = capture
	go capture.monitor()
	return nil
}

// CaptureFailedStartup binds stderr to the validated destination when normal
// initialization has failed. The process is exiting, so it neither rotates nor
// takes the lock that may itself have caused the failure.
func CaptureFailedStartup(path string) error {
	stderrMu.Lock()
	defer stderrMu.Unlock()
	if stderrCapture != nil {
		if stderrCapture.path != path {
			return fmt.Errorf("process output already captured at %s", stderrCapture.path)
		}
		return nil
	}
	file, err := openFailedStartupGeneration(path)
	if err != nil {
		return err
	}
	if _, err := regularFile(file); err != nil {
		return errors.Join(err, file.Close())
	}
	capture := &rawCapture{path: path, file: file, run: fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())}
	if err := capture.writeHeader(file); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := redirectStderr(file); err != nil {
		return errors.Join(err, file.Close())
	}
	stderrCapture = capture
	return nil
}

// openFailedStartupGeneration chooses the current staged generation only after
// validating its committed marker. A missing stage requires an existing
// published current generation.
func openFailedStartupGeneration(path string) (file *os.File, resultErr error) {
	data, err := readLegacyMarker(path + ".legacy-commit")
	if errors.Is(err, os.ErrNotExist) {
		return openFailedStartup(path)
	}
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Sources  json.RawMessage
		Previous *bool
	}
	if decodeErr := json.Unmarshal(data, &decoded); decodeErr != nil {
		return nil, fmt.Errorf("decode migration marker: %w", decodeErr)
	}
	if len(decoded.Sources) == 0 || decoded.Previous == nil {
		return nil, errors.New("migration marker missing required fields")
	}
	var sources []LegacySource
	if decodeErr := json.Unmarshal(decoded.Sources, &sources); decodeErr != nil {
		return nil, fmt.Errorf("decode migration sources: %w", decodeErr)
	}
	stage, err := openExistingFailedStartup(path + ".legacy-stage")
	if err == nil {
		return stage, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open staged diagnostics: %w", err)
	}
	current, err := openExistingFailedStartup(path)
	if err != nil {
		return nil, fmt.Errorf("open published diagnostics: %w", err)
	}
	return current, nil
}

func (capture *rawCapture) redirect(file *os.File) error {
	if capture.stdout {
		if err := redirectStdout(file); err != nil {
			return err
		}
	}
	return redirectStderr(file)
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
	failures := 0
	skip := 0
	for range ticker.C {
		if skip > 0 {
			skip--
			continue
		}
		err := capture.rotate()
		if err == nil {
			if failures > 0 {
				_, _ = fmt.Fprintf(Stderr, "stderr rotation recovered after %d failed attempts\n", failures)
			}
			failures = 0
			continue
		}
		// A transient failure (for example "too many open files") must not
		// stop rotation for the rest of the process lifetime. Retry with a
		// capped back-off and report only the first failure of a streak and
		// every power-of-two attempt after it, so the log is not flooded.
		failures++
		if failures&(failures-1) == 0 {
			// Direct stderr only: never recurse through the structured sink.
			_, _ = fmt.Fprintf(Stderr, "stderr rotation failed (attempt %d), will retry: %v\n", failures, err)
		}
		skip = rotationRetryDelayTicks(failures)
	}
}

// rotationRetryDelayTicks returns how many 100 ms monitor ticks to skip after
// the given number of consecutive failures: 1 s, 2 s, 4 s, ... capped at 30 s.
func rotationRetryDelayTicks(failures int) int {
	const maxTicks = 300
	if failures >= 6 {
		return maxTicks
	}
	return 10 << (failures - 1)
}

func (capture *rawCapture) rotate() error {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return withFileLock(capture.path, func() error {
		info, err := capture.file.Stat()
		if err != nil {
			return err
		}
		if !capture.pending && info.Size() < capture.limit {
			return nil
		}
		if !capture.pending {
			if rotateErr := rotateFiles(capture.path); rotateErr != nil {
				return rotateErr
			}
			capture.pending = true
		}
		open := capture.openReplacement
		if open == nil {
			open = openAppend
		}
		next, err := open(capture.path)
		if err != nil {
			return err
		}
		prepare := capture.prepareReplacement
		if prepare == nil {
			prepare = preservePermissions
		}
		if err := prepare(next, info); err != nil {
			return errors.Join(err, next.Close())
		}
		if err := capture.writeHeader(next); err != nil {
			return errors.Join(err, next.Close())
		}
		redirect := capture.redirectReplacement
		if redirect == nil {
			redirect = capture.redirect
		}
		if err := redirect(next); err != nil {
			return errors.Join(err, next.Close())
		}
		previous := capture.file
		capture.file = next
		capture.pending = false
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
