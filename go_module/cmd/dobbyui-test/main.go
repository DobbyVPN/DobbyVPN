//go:build !(android || ios)

// dobbyui-test is a deliberately small, headless UI companion. It drives the
// production Fyne widgets with Fyne's test driver, while the widget's client
// remains the real authenticated desktop gRPC client. The functional harness
// owns VPN observations; this process owns only UI actions and presentation.
package main

import (
	"bufio"
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
		application.Connection.Input.SetText("")
		// Real provider profiles can be hundreds of kilobytes. Fyne's test
		// driver emits one rune event at a time, making that input path take
		// minutes on Windows. Set the production entry's value atomically,
		// then exercise the real Connect widget callback below.
		application.Connection.Input.SetText(request.Config)
		test.Tap(application.Connection.Connect)
		if err := waitFor(application, "Connected", timeout(request.Timeout)); err != nil {
			return response{Error: err.Error(), Status: application.Connection.Status.Text, Details: application.Connection.Details.Text, Button: application.Connection.Connect.Text}
		}
		return snapshot(application)
	case "disconnect":
		test.Tap(application.Connection.Connect)
		if err := waitForAny(application, []string{"Disconnected", "Failed", "Error"}, timeout(request.Timeout)); err != nil {
			return response{Error: err.Error(), Status: application.Connection.Status.Text, Details: application.Connection.Details.Text, Button: application.Connection.Connect.Text}
		}
		return snapshot(application)
	case "wait":
		if strings.TrimSpace(request.State) == "" {
			return response{Error: "wait requires state"}
		}
		if err := waitFor(application, request.State, timeout(request.Timeout)); err != nil {
			return response{Error: err.Error(), Status: application.Connection.Status.Text, Details: application.Connection.Details.Text, Button: application.Connection.Connect.Text}
		}
		return snapshot(application)
	case "close":
		application.Close()
		return response{OK: true, Status: application.Connection.Status.Text, Details: application.Connection.Details.Text, Button: application.Connection.Connect.Text}
	default:
		return response{Error: fmt.Sprintf("unsupported operation %q", request.Op)}
	}
}

func snapshot(application *ui.Application) response {
	return response{OK: true, Status: application.Connection.Status.Text, Details: application.Connection.Details.Text, Button: application.Connection.Connect.Text}
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
		for _, state := range states {
			if application.Connection.Status.Text == state {
				return nil
			}
		}
		if application.Connection.Status.Text == "Error" || application.Connection.Status.Text == "Failed" {
			return fmt.Errorf("UI entered %s: %s", application.Connection.Status.Text, application.Connection.Details.Text)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for UI state %q (current %q)", strings.Join(states, ","), application.Connection.Status.Text)
}
