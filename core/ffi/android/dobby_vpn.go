//go:build android

package dobbyvpn

import (
	"core/sessionapi/mobilebinding"
)

var mobileSessions = mobilebinding.New(nil)

func init() { installJNIPlatform(mobileSessions) }
