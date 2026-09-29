//go:build !windows

package main

import (
	"core/desktop/controljson"
	"core/desktop/controlplane"
)

func dialService() controljson.Client {
	return controljson.Client{Dial: controlplane.DialDesktopControl}
}
