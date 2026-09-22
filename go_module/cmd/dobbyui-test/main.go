//go:build !(android || ios)

// dobbyui-test is a deliberately small, headless UI companion. It drives the
// production Fyne widgets with Fyne's test driver, while the widget's client
// remains the real authenticated desktop gRPC client. The functional harness
// owns VPN observations; this process owns only UI actions and presentation.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"go_module/desktop_exports/client"
	"go_module/grpcproto"
	"go_module/ui"
)

const defaultTimeout = 180 * time.Second

type request struct {
	Op      string `json:"op"`
	Config  string `json:"config,omitempty"`
	State   string `json:"state,omitempty"`
	Timeout int    `json:"timeout_ms,omitempty"`
	Path    string `json:"path,omitempty"`
	Label   string `json:"label,omitempty"`
}

type response struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Status  string `json:"status,omitempty"`
	Details string `json:"details,omitempty"`
	Button  string `json:"button,omitempty"`
	Path    string `json:"path,omitempty"`
	MIME    string `json:"mime,omitempty"`
	SHA256  string `json:"sha256,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "dobbyui-test: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	connection, err := client.Dial()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "dobbyui-test: close service connection: %v\n", closeErr)
		}
	}()

	runtime := newSerializedTestApp()
	defer runtime.close()
	serviceClient := ui.NewGRPCClient(grpcproto.NewVpnClient(connection))
	application := ui.NewApplication(runtime, serviceClient)
	application.Start()
	primeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := application.Connection.Prime(primeContext); err != nil {
		cancel()
		return fmt.Errorf("prime service session: %w", err)
	}
	cancel()
	fyne.DoAndWait(func() {})

	return serve(os.Stdin, os.Stdout, application)
}

func serve(input io.Reader, output io.Writer, application *ui.Application) error {
	decoder := json.NewDecoder(bufio.NewReader(input))
	encoder := json.NewEncoder(output)
	for {
		var req request
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				fyne.DoAndWait(application.Close)
				return nil
			}
			return err
		}
		result := handle(req, application)
		if err := encoder.Encode(result); err != nil {
			return err
		}
		if req.Op == "close" {
			return nil
		}
	}
}

func handle(request request, application *ui.Application) response {
	switch request.Op {
	case "state":
		return snapshot(application)
	case "connect":
		setInput(application, request.Config)
		fyne.DoAndWait(func() { test.Tap(application.Connection.Connect) })
		if err := waitFor(application, "Connected", timeout(request.Timeout)); err != nil {
			return errorSnapshot(application, err)
		}
		return snapshot(application)
	case "configure":
		setInput(application, request.Config)
		if err := application.Connection.Configure(context.Background(), []byte(request.Config)); err != nil {
			return errorSnapshot(application, err)
		}
		return snapshot(application)
	case "disconnect":
		fyne.DoAndWait(func() { test.Tap(application.Connection.Connect) })
		if err := waitForAny(application, []string{"Disconnected", "Failed", "Error"}, timeout(request.Timeout)); err != nil {
			return errorSnapshot(application, err)
		}
		return snapshot(application)
	case "wait":
		if strings.TrimSpace(request.State) == "" {
			return response{Error: "wait requires state"}
		}
		if err := waitFor(application, request.State, timeout(request.Timeout)); err != nil {
			return errorSnapshot(application, err)
		}
		return snapshot(application)
	case "capture":
		return captureResponse(application, request.Path, request.Label)
	case "capture-settings":
		fyne.DoAndWait(func() { test.Tap(application.Connection.Settings) })
		result := captureResponse(application, request.Path, request.Label)
		fyne.DoAndWait(func() { test.Tap(application.Settings.Back) })
		return result
	case "close":
		fyne.DoAndWait(application.Close)
		return snapshot(application)
	default:
		return response{Error: fmt.Sprintf("unsupported operation %q", request.Op)}
	}
}

func captureResponse(application *ui.Application, directory, label string) response {
	metadata, err := capture(application, directory, label)
	if err != nil {
		return errorSnapshot(application, err)
	}
	value := snapshot(application)
	value.Path = metadata.Path
	value.MIME = "image/png"
	value.SHA256 = metadata.SHA256
	value.Bytes = metadata.Bytes
	value.Width = metadata.Width
	value.Height = metadata.Height
	return value
}

type captureMetadata struct {
	Label  string `json:"label"`
	Path   string `json:"path"`
	MIME   string `json:"mime"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func capture(application *ui.Application, directory, label string) (captureMetadata, error) {
	if !filepath.IsAbs(directory) {
		return captureMetadata{}, fmt.Errorf("capture path must be absolute")
	}
	if !safeCaptureLabel(label) {
		return captureMetadata{}, fmt.Errorf("capture label is invalid")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return captureMetadata{}, fmt.Errorf("create capture directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return captureMetadata{}, fmt.Errorf("protect capture directory: %w", err)
	}

	var captured image.Image
	var masks []image.Rectangle
	fyne.DoAndWait(func() {
		canvas := application.Window.Canvas()
		captured = canvas.Capture()
		if application.Window.Content() == application.Connection.Content() {
			for _, object := range []fyne.CanvasObject{
				application.Connection.Input,
				application.Connection.Details,
				application.Connection.Logs,
				application.Connection.LogStatus,
			} {
				position := application.App.Driver().AbsolutePositionForObject(object)
				end := position.Add(object.Size())
				x0, y0 := canvas.PixelCoordinateForPosition(position)
				x1, y1 := canvas.PixelCoordinateForPosition(end)
				masks = append(masks, image.Rect(x0, y0, x1, y1))
			}
		}
	})
	if captured == nil || captured.Bounds().Empty() {
		return captureMetadata{}, fmt.Errorf("capture returned no pixels")
	}
	masked := image.NewNRGBA(captured.Bounds())
	draw.Draw(masked, masked.Bounds(), captured, captured.Bounds().Min, draw.Src)
	for _, rectangle := range masks {
		rectangle = rectangle.Add(masked.Bounds().Min).Intersect(masked.Bounds())
		if !rectangle.Empty() {
			draw.Draw(masked, rectangle, image.NewUniform(color.NRGBA{A: 0xff}), image.Point{}, draw.Src)
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, masked); err != nil {
		return captureMetadata{}, fmt.Errorf("encode capture: %w", err)
	}
	digest := sha256.Sum256(encoded.Bytes())
	path := filepath.Join(directory, label+".png")
	if err := writeCaptureFile(path, encoded.Bytes()); err != nil {
		return captureMetadata{}, fmt.Errorf("write capture: %w", err)
	}
	metadata := captureMetadata{
		Label: label, Path: path, MIME: "image/png", SHA256: fmt.Sprintf("%x", digest),
		Bytes: encoded.Len(), Width: masked.Bounds().Dx(), Height: masked.Bounds().Dy(),
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return captureMetadata{}, fmt.Errorf("encode capture metadata: %w", err)
	}
	metadataBytes = append(metadataBytes, '\n')
	if err := writeCaptureFile(filepath.Join(directory, label+".json"), metadataBytes); err != nil {
		return captureMetadata{}, fmt.Errorf("write capture metadata: %w", err)
	}
	return metadata, nil
}

func writeCaptureFile(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if count, err := file.Write(payload); err != nil {
		writeErr = err
	} else if count != len(payload) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return writeErr
	}
	return closeErr
}

func safeCaptureLabel(label string) bool {
	if label == "" {
		return false
	}
	for _, value := range label {
		if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') || value == '-' || value == '_' {
			continue
		}
		return false
	}
	return true
}

func setInput(application *ui.Application, config string) {
	if config == "" {
		return
	}
	// Real provider profiles can be hundreds of kilobytes. Keep the exact
	// source in the production entry's staged-source seam while showing only
	// its fixed summary; rendering the whole document makes software-driver
	// screenshots arbitrarily slow and would make capture time depend on
	// provider profile size.
	fyne.DoAndWait(func() {
		if application.Connection.Input.SourceText() != config {
			application.Connection.Input.SetSourceText(config)
		}
	})
}

func snapshot(application *ui.Application) response {
	status, details, button := application.Connection.Presentation()
	return response{OK: true, Status: status, Details: details, Button: button}
}

func errorSnapshot(application *ui.Application, err error) response {
	value := snapshot(application)
	value.OK = false
	value.Error = err.Error()
	return value
}

func timeout(milliseconds int) time.Duration {
	if milliseconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func waitFor(application *ui.Application, state string, limit time.Duration) error {
	return waitForAny(application, []string{state}, limit)
}

func waitForAny(application *ui.Application, states []string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		status, details, _ := application.Connection.Presentation()
		for _, state := range states {
			if status == state {
				return nil
			}
		}
		if status == "Error" || status == "Failed" {
			return fmt.Errorf("UI entered %s: %s", status, details)
		}
		time.Sleep(50 * time.Millisecond)
	}
	status, _, _ := application.Connection.Presentation()
	return fmt.Errorf("timed out waiting for UI state %q (current %q)", strings.Join(states, ","), status)
}
