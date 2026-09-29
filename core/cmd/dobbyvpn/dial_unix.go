//go:build !windows

package main

import (
	"core/desktop_exports/controljson"
	"core/desktop_exports/controlplane"
)

func dialService() controljson.Client {
	return controljson.Client{Dial: controlplane.DialDesktopControl}
}
