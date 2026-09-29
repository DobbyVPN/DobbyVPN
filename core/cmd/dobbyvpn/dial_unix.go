//go:build !windows

package main

import (
	"core/clientserver/controljson"
	"core/clientserver/controlplane"
)

func dialService() controljson.Client {
	return controljson.Client{Dial: controlplane.DialDesktopControl}
}
