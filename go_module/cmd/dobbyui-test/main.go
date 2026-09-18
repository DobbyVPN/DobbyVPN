//go:build !(android || ios)

// dobbyui-test is a deliberately small, headless UI companion. It drives the
// production Fyne widgets with Fyne's test driver, while the widget's client
// remains the real authenticated desktop gRPC client. The functional harness
// owns VPN observations; this process owns only UI actions and presentation.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
}

type response struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Status  string `json:"status,omitempty"`
	Details string `json:"details,omitempty"`
	Button  string `json:"button,omitempty"`
}

func main() {
	connection, err := client.Dial()
	if err != nil {
		fatal(err)
	}
	defer connection.Close()

	runtime := test.NewApp()
	defer runtime.Quit()
	serviceClient := ui.NewGRPCClient(grpcproto.NewVpnClient(connection))
	application := ui.NewApplication(runtime, serviceClient)
	application.Start()
	primeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := application.Connection.Prime(primeContext); err != nil {
		cancel()
		fatal(fmt.Errorf("prime service session: %w", err))
	}
	cancel()

	if err := serve(os.Stdin, os.Stdout, application); err != nil {
		fmt.Fprintf(os.Stderr, "dobbyui-test: %v\n", err)
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "dobbyui-test: %v\n", err)
	os.Exit(1)
}

func serve(input io.Reader, output io.Writer, application *ui.Application) error {
	decoder := json.NewDecoder(bufio.NewReader(input))
	encoder := json.NewEncoder(output)
	for {
		var request request
		if err := decoder.Decode(&request); err != nil {
			if err == io.EOF {
				application.Close()
				return nil
			}
			return err
		}
		result := handle(request, application)
		if err := encoder.Encode(result); err != nil {
			return err
		}
		if request.Op == "close" {
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
		test.Tap(application.Connection.Connect)
		if err := waitFor(application, "Connected", timeout(request.Timeout)); err != nil {
			return errorSnapshot(application, err)
		}
		return snapshot(application)
	case "configure":
		setInput(application, request.Config)
		if err := application.Connection.Configure(context.Background(), []byte(application.Connection.Input.Text)); err != nil {
			return errorSnapshot(application, err)
		}
		return snapshot(application)
	case "disconnect":
		test.Tap(application.Connection.Connect)
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
	case "close":
		application.Close()
		return snapshot(application)
	default:
		return response{Error: fmt.Sprintf("unsupported operation %q", request.Op)}
	}
}

func setInput(application *ui.Application, config string) {
	if config == "" || application.Connection.Input.Text == config {
		return
	}
	// Real provider profiles can be hundreds of kilobytes. Fyne's test
	// driver's rune-by-rune typing is intentionally avoided; SetText updates
	// the production entry atomically and the subsequent operation still uses
	// the real widget callback where applicable.
	application.Connection.Input.SetText(config)
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
