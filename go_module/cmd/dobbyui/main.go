//go:build !(android || ios)

package main

import (
	"fmt"
	"os"

	"fyne.io/fyne/v2/app"

	"go_module/desktop_exports/client"
	"go_module/grpcproto"
	"go_module/ui"
)

func main() {
	connection, err := client.Dial()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not connect to DobbyVPN service:", err)
		os.Exit(1)
	}
	defer connection.Close()

	client := ui.NewGRPCClient(grpcproto.NewVpnClient(connection))
	store, err := ui.NewFileSourceStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not initialize connection source storage:", err)
		os.Exit(1)
	}
	ui.NewApplication(app.NewWithID("com.dobby.vpn"), client, store).Run()
}
