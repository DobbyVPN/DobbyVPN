// Package diagnostics owns retained file generations and streaming collection.
package diagnostics

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// Threshold is a rotation trigger, not a truncation limit. The write which
// crosses it is retained in full, so either generation may exceed this size.
const Threshold int64 = 150_000_000
const PreviousSuffix = ".previous"

var processFiles sync.Map // Serializes threads as well as OS advisory locks.

func fileMutex(path string) *sync.Mutex {
	value, _ := processFiles.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	return value.(*sync.Mutex)
}

// withFileLock coordinates complete records and generation snapshots. The lock
// holds only while opening input handles, never during export compression.
func withFileLock(path string, operation func() error) (resultErr error) {
	mutex := fileMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	lock, err := openLock(path + ".lock")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	// #nosec G703 -- Diagnostic paths come from local logger configuration or app-private directories, not remote control input.
	if info, err := os.Stat(path); err == nil {
		if err := preservePermissions(lock, info); err != nil {
			return err
		}
	}
	if err := lockFile(lock); err != nil {
		return fmt.Errorf("lock diagnostic file %s: %w", path, err)
	}
	defer func() {
		if err := unlockFile(lock); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("unlock diagnostic file %s: %w", path, err))
		}
	}()
	return operation()
}

func regularFile(file *os.File) (os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("diagnostic input is not a regular file: %s", file.Name())
	}
	return info, nil
}

func removeIfPresent(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Writer accepts one complete structured record per Write. Opening an existing
// history normalizes oversized generations using bounded streaming migration.
type Writer struct {
	path   string
	limit  int64
	closed bool
}

func OpenWriter(path string) (*Writer, error) { return openWriter(path, Threshold) }
func openWriter(path string, limit int64) (*Writer, error) {
	if limit <= 0 {
		return nil, errors.New("rotation threshold must be positive")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if mkdirErr := os.MkdirAll(filepath.Dir(absolute), 0o700); mkdirErr != nil {
		return nil, mkdirErr
	}
	w := &Writer{path: absolute, limit: limit}
	err = withFileLock(absolute, func() error {
		if migrateErr := migrateHistory(absolute, limit); migrateErr != nil {
			return migrateErr
		}
		file, openErr := openAppend(absolute)
		if openErr != nil {
			return openErr
		}
		return file.Close()
	})
	return w, err
}

func (w *Writer) Close() error {
	mutex := fileMutex(w.path)
	mutex.Lock()
	defer mutex.Unlock()
	w.closed = true
	return nil
}

func (w *Writer) Write(record []byte) (int, error) {
	return w.WriteRecord(func(bool) ([]byte, error) { return record, nil })
}

func (w *Writer) WriteRecord(encode func(firstInGeneration bool) ([]byte, error)) (count int, resultErr error) {
	resultErr = withFileLock(w.path, func() (err error) {
		if w.closed {
			return os.ErrClosed
		}
		file, err := openAppend(w.path)
		if err != nil {
			return err
		}
		info, err := regularFile(file)
		if err != nil {
			return errors.Join(err, file.Close())
		}
		first := info.Size() == 0
		if info.Size() >= w.limit {
			first = true
			if closeErr := file.Close(); closeErr != nil {
				return closeErr
			}
			if rotateErr := rotateFiles(w.path); rotateErr != nil {
				return rotateErr
			}
			file, err = openAppend(w.path)
			if err != nil {
				return err
			}
			if modeErr := preservePermissions(file, info); modeErr != nil {
				return errors.Join(modeErr, file.Close())
			}
		}
		defer func() { err = errors.Join(err, file.Close()) }()
		record, encodeErr := encode(first)
		if encodeErr != nil {
			return encodeErr
		}
		count, err = file.Write(record)
		if err == nil && count != len(record) {
			err = io.ErrShortWrite
		}
		return err
	})
	return count, resultErr
}

func rotateFiles(path string) error {
	if err := removeIfPresent(path + PreviousSuffix); err != nil {
		return err
	}
	return os.Rename(path, path+PreviousSuffix)
}

func (w *Writer) Sync() error {
	return withFileLock(w.path, func() (resultErr error) {
		file, err := openAppend(w.path)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
		return file.Sync()
	})
}

func withSnapshotLock(path string, operation func() error) (resultErr error) {
	mutex := fileMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	lock, err := OpenInput(path + ".lock")
	if errors.Is(err, os.ErrNotExist) {
		return operation()
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, lock.Close()) }()
	if err := readLockFile(lock); err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, unlockFile(lock)) }()
	return operation()
}

func (w *Writer) Path() string { return w.path }
