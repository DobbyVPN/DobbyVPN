//go:build !windows

package main

import (
	"go_module/desktop_exports/controljson"
	"go_module/desktop_exports/controlplane"
)

func dialService() controljson.Client {
	return controljson.Client{Dial: controlplane.DialDesktopControl}
}
