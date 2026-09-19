//go:build !(android || ios)

package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"go_module/ui"
)

// desktopLogExporter keeps the native file picker out of the shared UI. The
// UI supplies only the input-safe diagnostic lines it displays; the selected
// destination is always chosen explicitly by the user.
type desktopLogExporter struct {
	window fyne.Window
}

func newDesktopLogExporter(window fyne.Window) ui.LogExporter {
	return &desktopLogExporter{window: window}
}

func (e *desktopLogExporter) Export(ctx context.Context, lines []string) error {
	if e == nil || e.window == nil {
		return fmt.Errorf("desktop log export window is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload := strings.Join(append([]string(nil), lines...), "\n")
	destination := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil {
			fyne.LogError("choose log export destination", err)
			return
		}
		if writer == nil {
			return
		}
		defer writer.Close()
		if err := ctx.Err(); err != nil {
			return
		}
		if err := writeGzip(writer, payload); err != nil {
			fyne.LogError("write log export", err)
		}
	}, e.window)
	destination.SetTitleText("Export logs")
	destination.SetFileName(defaultLogExportName(time.Now()))
	destination.Show()
	return nil
}

func defaultLogExportName(now time.Time) string {
	return "DobbyVPN_logs_" + now.Format("2006-01-02_15-04-05") + ".jsonl.gz"
}

func writeGzip(destination io.Writer, payload string) error {
	archive, err := gzip.NewWriterLevel(destination, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(archive, payload); err != nil {
		_ = archive.Close()
		return err
	}
	return archive.Close()
}
