package diagnostics

import (
	"bufio"
	"errors"
	"io"
	"os"
)

// Migrate only oversized histories. A record is streamed in pieces when it is
// larger than the buffer; rotation occurs solely between records.
func migrateHistory(path string, limit int64) error {
	// Resume an interrupted publication before inspecting the visible files.
	if _, err := os.Stat(path + ".legacy-commit"); err == nil {
		return migrateLegacyTransaction(path, nil, limit, true)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	oversized, _, err := historyNeedsMigration(path, limit)
	if err != nil || !oversized {
		return err
	}
	return migrateLegacyTransaction(path, nil, limit, true)
}

func historyNeedsMigration(path string, limit int64) (needsMigration bool, original os.FileInfo, resultErr error) {
	for _, name := range []string{path + PreviousSuffix, path} {
		info, err := os.Stat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, nil, err
		}
		if !info.Mode().IsRegular() {
			return false, nil, &os.PathError{Op: "migrate diagnostic", Path: name, Err: os.ErrInvalid}
		}
		if info.Size() > limit {
			recordAfterLimit, scanErr := hasRecordAfterLimit(name, limit, info.Size())
			if scanErr != nil {
				return false, nil, scanErr
			}
			needsMigration = needsMigration || recordAfterLimit
		}
		original = info
	}
	return needsMigration, original, nil
}

// A complete record may cross the rotation threshold because WriteRecord
// rotates only before the next write. Preserve that generation (and its file
// identity) when the crossing record is the final record in the file. A
// newline after the crossing record proves there is additional history to
// normalize. Exclude the final byte so a newline exactly at EOF is valid.
func hasRecordAfterLimit(path string, limit, size int64) (found bool, resultErr error) {
	file, err := OpenInput(path)
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	reader := bufio.NewReaderSize(io.NewSectionReader(file, limit-1, size-limit), 64*1024)
	for {
		_, err := reader.ReadSlice('\n')
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return false, nil
		default:
			return false, err
		}
	}
}

func streamMigration(input io.Reader, path string, limit int64, size *int64, atStart *bool) (resultErr error) {
	return streamMigrationLabeled(input, path, limit, size, atStart, nil)
}

// A source marker is written with the first record in every retained generation.
func streamMigrationLabeled(input io.Reader, path string, limit int64, size *int64, atStart *bool, header []byte) (resultErr error) {
	reader := bufio.NewReaderSize(input, 64*1024)
	needHeader := len(header) != 0
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
			needHeader = len(header) != 0
		}
		if needHeader {
			n, headerErr := output.Write(header)
			*size += int64(n)
			if headerErr != nil {
				return headerErr
			}
			if n != len(header) {
				return io.ErrShortWrite
			}
			needHeader = false
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
