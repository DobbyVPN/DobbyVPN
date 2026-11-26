//go:build !(android || ios)

package executor

import (
	"context"
	"io"
	"runtime"

	"core/clientserver/controljson"
	"core/diagnostics"
	"core/log"
	sessionruntime "core/sessionapi/runtime"

	"errors"
	"fmt"
)

const darwinPlatform = "darwin"

// localLogPath is the resolved backend log destination this process writes.
// The authenticated control socket serves it to the CLI for export and viewing.
var localLogPath string

type Executor struct {
}

// recoverInterruptedState is a seam for the startup-path test.
var recoverInterruptedState = sessionruntime.RecoverInterruptedState

// Capture raw output before opening the structured sink so its own startup
// failures survive. Validate the supervisor's path before creating the destination.
func initExplicitLocalLog() (resultErr error) {
	path, err := prepareLocalLogPath()
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			if err := diagnostics.CaptureFailedStartup(path); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("capture failed startup diagnostics: %w", err))
			}
		}
	}()
	sources := []diagnostics.LegacySource{{Path: path + ".stderr", Label: "stderr"}}
	if runtime.GOOS == darwinPlatform {
		sources = append(sources, diagnostics.LegacySource{Path: path + ".stdout", Label: "stdout"})
	}
	if err := diagnostics.MigrateLegacy(path, sources); err != nil {
		return fmt.Errorf("migrate backend diagnostics: %w", err)
	}
	var captureErr error
	if runtime.GOOS == darwinPlatform {
		captureErr = diagnostics.CaptureOutput(path, path)
	} else {
		captureErr = diagnostics.CaptureStderr(path, path)
	}
	if captureErr != nil {
		return fmt.Errorf("capture backend output: %w", captureErr)
	}
	if err := log.SetAppenderPath(path); err != nil {
		return err
	}
	localLogPath = path
	return nil
}

// captureServiceLogs serves this service's own retained log generations. It
// never reads a client-supplied path, so the call cannot widen file access.
func captureServiceLogs(ctx context.Context, since map[string]int64, metadataOnly bool) (*controljson.StreamExport, error) {
	path := localLogPath
	if path == "" {
		return nil, errors.New("service log path is unavailable")
	}
	snapshot, captureErr := diagnostics.Capture([]string{path})
	if captureErr != nil {
		return nil, errors.Join(captureErr, snapshot.Close())
	}
	segments := make([]controljson.StreamSegment, 0, len(snapshot.Inputs))
	var total int64
	for _, input := range snapshot.Inputs {
		offset := since[input.ID]
		if offset < 0 {
			offset = 0
		}
		if offset > input.Size {
			offset = input.Size
		}
		segments = append(segments, controljson.StreamSegment{Path: input.Path, ID: input.ID, Size: input.Size, Offset: offset})
		total += input.Size - offset
	}
	export := &controljson.StreamExport{
		Result: controljson.StreamResult{Segments: segments, Bytes: total},
		Close:  snapshot.Close,
	}
	if metadataOnly || total == 0 {
		return export, nil
	}
	export.Write = func(output io.Writer) error {
		writer := diagnostics.ContextWriter{Ctx: ctx, Writer: output}
		for index, input := range snapshot.Inputs {
			if _, err := input.CopyTo(writer, segments[index].Offset); err != nil {
				return err
			}
		}
		return nil
	}
	return export, nil
}
