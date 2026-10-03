package diagnostics

import (
	"bufio"
	"errors"
	"io"
	"os"
)

// Migrate only oversized histories. A record is streamed in pieces when it is
// larger than the buffer; rotation occurs solely between records.
func migrateHistory(path string, limit int64) (resultErr error) {
	oversized := false
	var original os.FileInfo
	for _, name := range []string{path + PreviousSuffix, path} {
		info, err := os.Stat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return &os.PathError{Op: "migrate diagnostic", Path: name, Err: os.ErrInvalid}
		}
		oversized = oversized || info.Size() > limit
		original = info
	}
	if !oversized {
		return nil
	}
	stage := path + ".migration"
	if err := removeIfPresent(stage); err != nil {
		return err
	}
	if err := removeIfPresent(stage + PreviousSuffix); err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, removeIfPresent(stage), removeIfPresent(stage+PreviousSuffix))
	}()
	var size int64
	atStart := true
	for _, name := range []string{path + PreviousSuffix, path} {
		input, err := OpenInput(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		err = streamMigration(input, stage, limit, &size, &atStart)
		if closeErr := errors.Join(err, input.Close()); closeErr != nil {
			return closeErr
		}
	}
	// Inputs are never truncated. Readers holding their old handles keep the
	// captured bytes through both renames.
	if err := removeIfPresent(path + PreviousSuffix); err != nil {
		return err
	}
	for _, suffix := range []string{PreviousSuffix, ""} {
		file, err := openExistingAppend(stage + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		err = preservePermissions(file, original)
		if closeErr := errors.Join(err, file.Close()); closeErr != nil {
			return closeErr
		}
		if suffix != "" {
			if err := os.Rename(stage+suffix, path+suffix); err != nil {
				return err
			}
		}
	}

	return os.Rename(stage, path)
}

func streamMigration(input io.Reader, path string, limit int64, size *int64, atStart *bool) (resultErr error) {
	reader := bufio.NewReaderSize(input, 64*1024)
	output, err := openAppend(path)
	if err != nil {
		return err
	}
	defer func() {
		if output != nil {
			resultErr = errors.Join(resultErr, output.Close())
		}
	}()
	for {
		record, readErr := reader.ReadSlice('\n')

		if len(record) == 0 {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
		if *atStart && *size >= limit {
			if closeErr := output.Close(); closeErr != nil {
				output = nil
				return closeErr
			}
			output = nil
			if rotateErr := rotateFiles(path); rotateErr != nil {
				return rotateErr
			}
			output, err = openAppend(path)
			if err != nil {
				return err
			}
			*size = 0
		}
		n, writeErr := output.Write(record)
		*size += int64(n)
		if writeErr != nil {
			return writeErr
		}
		if n != len(record) {
			return io.ErrShortWrite
		}
		*atStart = record[len(record)-1] == '\n'

		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) {
			return readErr
		}
	}
}

func openExistingAppend(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
}
