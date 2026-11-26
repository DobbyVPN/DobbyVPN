package diagnostics

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Input is a stable handle and length captured before any export copying.
// The writer may rename or unlink its generation without changing these bytes.
type Input struct {
	Path string
	File *os.File
	Size int64
	ID   string
}

type Snapshot struct{ Inputs []Input }

func Capture(paths []string) (snapshot *Snapshot, resultErr error) {
	snapshot = &Snapshot{}
	seen := map[string]bool{}
	var failures []error
	for _, path := range paths {
		absolute, pathErr := filepath.Abs(path)
		if pathErr != nil {
			failures = append(failures, pathErr)
			continue
		}
		captureErr := withSnapshotLock(absolute, func() error {
			for _, name := range []string{absolute + PreviousSuffix, absolute} {
				file, err := OpenInput(name)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					failures = append(failures, fmt.Errorf("open %s: %w", name, err))
					continue
				}
				info, err := regularFile(file)
				if err != nil {
					failures = append(failures, errors.Join(err, file.Close()))
					continue
				}
				id, err := Identity(file)
				if err != nil {
					failures = append(failures, errors.Join(err, file.Close()))
					continue
				}
				if seen[id] {
					failures = append(failures, file.Close())
					continue
				}
				seen[id] = true
				snapshot.Inputs = append(snapshot.Inputs, Input{name, file, info.Size(), id})
			}
			return nil
		})
		if captureErr != nil {
			failures = append(failures, fmt.Errorf("capture %s: %w", absolute, captureErr))
		}
	}
	return snapshot, errors.Join(failures...)
}
func (snapshot *Snapshot) Close() error {
	failures := make([]error, 0, len(snapshot.Inputs))
	for _, input := range snapshot.Inputs {
		failures = append(failures, input.File.Close())
	}
	snapshot.Inputs = nil
	return errors.Join(failures...)
}

func (input Input) CopyTo(output io.Writer, offset int64) (int64, error) {
	if offset < 0 || offset > input.Size {
		return 0, fmt.Errorf("invalid view offset %d for %s", offset, input.Path)
	}
	count, err := io.CopyBuffer(output, io.NewSectionReader(input.File, offset, input.Size-offset), make([]byte, 64*1024))
	if err == nil && count != input.Size-offset {
		err = fmt.Errorf("diagnostic input shortened: %s: %w", input.Path, io.ErrUnexpectedEOF)
	}
	return count, err
}

// ExportInput is one ordered export section: its path label and exactly Size
// bytes produced by CopyTo when the section is written.
type ExportInput struct {
	Path   string
	Size   int64
	CopyTo func(io.Writer) (int64, error)
}

// ExportInput converts a captured input into an export section from its start.
func (input Input) ExportInput() ExportInput {
	return ExportInput{
		Path: input.Path,
		Size: input.Size,
		CopyTo: func(output io.Writer) (int64, error) {
			return input.CopyTo(output, 0)
		},
	}
}

// ExportInputs returns the snapshot's sections in capture order.
func (snapshot *Snapshot) ExportInputs() []ExportInput {
	inputs := make([]ExportInput, 0, len(snapshot.Inputs))
	for _, input := range snapshot.Inputs {
		inputs = append(inputs, input.ExportInput())
	}
	return inputs
}

// ContextWriter stops writes at a chunk boundary once ctx is cancelled.
type ContextWriter struct {
	Ctx    context.Context
	Writer io.Writer
}

func (writer ContextWriter) Write(data []byte) (int, error) {
	if err := writer.Ctx.Err(); err != nil {
		return 0, err
	}
	return writer.Writer.Write(data)
}

// ExportGzip leaves a complete gzip only after every input and the destination
// close succeed. Missing streams are normal with a stopped backend.
func ExportGzip(destination string, inputs []ExportInput, header string) error {
	return ExportGzipContext(context.Background(), destination, inputs, header)
}

// ExportGzipContext stops copying at a chunk boundary when ctx is cancelled.
func ExportGzipContext(ctx context.Context, destination string, inputs []ExportInput, header string) (resultErr error) {
	var output *os.File
	defer func() {
		if output != nil {
			resultErr = errors.Join(resultErr, output.Close())
			if resultErr != nil {
				resultErr = errors.Join(resultErr, removeIfPresent(destination))
			}
		}
	}()
	// O_EXCL refuses existing user files, input files and symlinks.
	var err error
	output, err = os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	compressor := gzip.NewWriter(output)
	defer func() { resultErr = errors.Join(resultErr, compressor.Close()) }()
	writer := ContextWriter{Ctx: ctx, Writer: compressor}
	if _, err := io.WriteString(writer, header); err != nil {
		return err
	}
	for _, input := range inputs {
		if _, err := fmt.Fprintf(writer, "\n--- %s ---\n", input.Path); err != nil {
			return err
		}
		if _, err := input.CopyTo(writer); err != nil {
			return err
		}
	}
	return nil
}
