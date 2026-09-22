//go:build !(android || ios)

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"go_module/ui"
)

type captureClient struct{}

func (captureClient) Configure(context.Context, []byte, uint64) (ui.ConfigureResult, error) {
	return ui.ConfigureResult{}, nil
}
func (captureClient) Start(context.Context, uint64) (ui.StartResult, error) {
	return ui.StartResult{}, nil
}
func (captureClient) Stop(context.Context, uint64) (ui.StopResult, error) {
	return ui.StopResult{}, nil
}
func (captureClient) Snapshot(context.Context) (ui.Snapshot, error) {
	return ui.Snapshot{}, nil
}
func (captureClient) Watch(context.Context) (<-chan ui.Snapshot, error) {
	return make(chan ui.Snapshot), nil
}
func (captureClient) Reset(context.Context, uint64) (ui.Snapshot, error) {
	return ui.Snapshot{}, nil
}

func TestSafeCaptureLabel(t *testing.T) {
	for _, value := range []string{"001-startup", "failure_2", "ABC"} {
		if !safeCaptureLabel(value) {
			t.Fatalf("safeCaptureLabel(%q) = false", value)
		}
	}
	for _, value := range []string{"", "../secret", "has space", "path/name", "name.png"} {
		if safeCaptureLabel(value) {
			t.Fatalf("safeCaptureLabel(%q) = true", value)
		}
	}
}

func TestCaptureWritesNonblankIntegrityCheckedPngAndMasksSensitiveRegions(t *testing.T) {
	runtime := test.NewApp()
	defer runtime.Quit()
	application := ui.NewApplication(runtime, captureClient{})
	application.Window.Resize(fyne.NewSize(460, 520))
	application.Window.SetContent(application.Connection.Content())
	fyne.DoAndWait(func() {
		application.Connection.Input.SetText("SECRET-CONFIG")
		application.Connection.Details.SetText("SECRET-DETAIL")
		application.Connection.Logs.SetText("SECRET-LOG")
		application.Connection.LogStatus.SetText("SECRET-STATUS")
		application.Window.Show()
	})

	directory := t.TempDir()
	metadata, err := capture(application, directory, "masked")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(directory, "masked.png"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(payload)
	if metadata.Bytes != len(payload) || metadata.SHA256 != fmt.Sprintf("%x", digest) {
		t.Fatalf("metadata integrity = %#v, bytes=%d, sha256=%x", metadata, len(payload), digest)
	}
	imageValue, err := png.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if imageValue.Bounds().Dx() != metadata.Width || imageValue.Bounds().Dy() != metadata.Height {
		t.Fatalf("metadata dimensions = %dx%d, image = %v", metadata.Width, metadata.Height, imageValue.Bounds())
	}
	if nonBlackPixels(imageValue) == 0 {
		t.Fatal("capture is entirely masked/blank")
	}
	for _, object := range []fyne.CanvasObject{
		application.Connection.Input,
		application.Connection.Details,
		application.Connection.Logs,
		application.Connection.LogStatus,
	} {
		position := application.App.Driver().AbsolutePositionForObject(object)
		end := position.Add(object.Size())
		x0, y0 := application.Window.Canvas().PixelCoordinateForPosition(position)
		x1, y1 := application.Window.Canvas().PixelCoordinateForPosition(end)
		masked := image.Rect(x0, y0, x1, y1).Intersect(imageValue.Bounds())
		if masked.Empty() || blackPixels(imageValue, masked) != masked.Dx()*masked.Dy() {
			t.Fatalf("sensitive region was not completely masked: %v", masked)
		}
	}
	if _, err := capture(application, directory, "masked"); err == nil {
		t.Fatal("capture silently overwrote an existing per-run artifact")
	}
	invalidDirectory := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(invalidDirectory, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := capture(application, invalidDirectory, "unwritable"); err == nil {
		t.Fatal("capture accepted a non-directory output path")
	}
}

func blackPixels(imageValue image.Image, region image.Rectangle) int {
	count := 0
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			r, g, b, _ := imageValue.At(x, y).RGBA()
			if r == 0 && g == 0 && b == 0 {
				count++
			}
		}
	}
	return count
}

func nonBlackPixels(imageValue image.Image) int {
	return imageValue.Bounds().Dx()*imageValue.Bounds().Dy() -
		blackPixels(imageValue, imageValue.Bounds())
}
