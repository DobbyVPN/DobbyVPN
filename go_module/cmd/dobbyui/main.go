//go:build !(android || ios)

package main

import (
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	desktopclient "go_module/desktop_exports/client"
	"go_module/grpcproto"
	"go_module/ui"
)

func newDesktopApplication(runtime fyne.App, sessionClient ui.SessionClient, store ui.SourceStore) *ui.Application {
	application := ui.NewApplication(runtime, sessionClient, store)
	application.Connection.SetLogExporter(newDesktopLogExporter(application.Window))
	return application
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if err := installNSGLSoftwareFallback(); err != nil {
		return fmt.Errorf("could not initialize macOS OpenGL compatibility: %w", err)
	}

	connection, err := desktopclient.Dial()
	if err != nil {
		return fmt.Errorf("could not connect to DobbyVPN service: %w", err)
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "could not close DobbyVPN service connection:", closeErr)
		}
	}()

	sessionClient := ui.NewGRPCClient(grpcproto.NewVpnClient(connection))
	store, err := ui.NewFileSourceStore()
	if err != nil {
		return fmt.Errorf("could not initialize connection source storage: %w", err)
	}
	newDesktopApplication(app.NewWithID("com.dobby.vpn"), sessionClient, store).Run()
	return nil
}
