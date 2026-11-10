package diagnostics

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// A hang capture is one bounded all-goroutine snapshot written beside the
// process diagnostics. It is deliberately independent of the logger, the
// session cleanup transaction and the engine's stop locks: a blocked
// operation must still be dumpable and exportable while it is stuck.
const (
	// DumpMaxBytes caps one written dump, including its truncation marker.
	DumpMaxBytes = 1 << 20
	// DumpMaxFiles caps the retained dump set in one diagnostics directory.
	DumpMaxFiles = 4
	// DumpPrefix names every retained dump so exports and UI listings can
	// find them without a registry.
	DumpPrefix = "hang-dump-"
	// DumpTruncationMarker states truncation inside the dump itself, so a
	// reader never has to infer it from a missing tail.
	DumpTruncationMarker = "dump_truncated=true"
	dumpNameLayout       = "20060102T150405Z"
	dumpSuffix           = ".txt"
)

// dumpMaxBytes is a package variable so in-package tests can force
// truncation without generating a real megabyte of stacks.
var dumpMaxBytes int64 = DumpMaxBytes

var dumpSequence atomic.Uint64

// CaptureGoroutineDump writes one capped all-goroutine dump into dir and
// returns its base name. Truncation and profile failures are recorded inside
// the file; a failure with no bytes written returns without creating a file.
func CaptureGoroutineDump(dir string) (name string, truncated bool, err error) {
	if dir == "" {
		return "", false, errors.New("dump directory is unavailable")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", false, err
	}
	var buffer bytes.Buffer
	// Debug level two is the same all-goroutine form the inline slow-stop
	// diagnostics use, so readers find the same frames.
	profileErr := pprof.Lookup("goroutine").WriteTo(&buffer, 2)
	data := buffer.Bytes()
	if profileErr != nil && len(data) == 0 {
		return "", false, profileErr
	}
	truncated = profileErr != nil || int64(len(data)) > dumpMaxBytes
	if int64(len(data)) > dumpMaxBytes {
		data = data[:dumpMaxBytes]
	}
	payload := make([]byte, 0, len(data)+48)
	payload = append(payload, data...)
	if len(payload) > 0 && payload[len(payload)-1] != '\n' {
		payload = append(payload, '\n')
	}
	if truncated {
		payload = append(payload, DumpTruncationMarker...)
	} else {
		payload = append(payload, "dump_truncated=false"...)
	}
	payload = append(payload, '\n')

	if pruneErr := pruneDumps(dir); pruneErr != nil {
		return "", truncated, pruneErr
	}
	// One process may capture twice inside one second (a second stop, a second
	// slow outline close); the sequence keeps every name unique and ordered.
	name = DumpPrefix + time.Now().UTC().Format(dumpNameLayout) + "-" +
		strconv.Itoa(os.Getpid()) + "-" + strconv.FormatUint(dumpSequence.Add(1), 10) + dumpSuffix
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", truncated, err
	}
	written, writeErr := file.Write(payload)
	closeErr := file.Close()
	if writeErr == nil && written != len(payload) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", truncated, errors.Join(err, removeIfPresent(path))
	}
	return name, truncated, nil
}

// DumpFiles lists the retained dumps in dir, oldest first.
func DumpFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		value := entry.Name()
		if strings.HasPrefix(value, DumpPrefix) && strings.HasSuffix(value, dumpSuffix) {
			names = append(names, value)
		}
	}
	// The fixed-width UTC stamp in the name makes lexical order oldest first.
	sort.Strings(names)
	paths := make([]string, len(names))
	for index, value := range names {
		paths[index] = filepath.Join(dir, value)
	}
	return paths, nil
}

// pruneDumps removes the oldest dumps so one more fits within DumpMaxFiles.
func pruneDumps(dir string) error {
	existing, err := DumpFiles(dir)
	if err != nil {
		return err
	}
	for _, path := range existing[:max(0, len(existing)-(DumpMaxFiles-1))] {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return fmt.Errorf("retain %d dumps: %w", DumpMaxFiles, removeErr)
		}
	}
	return nil
}
