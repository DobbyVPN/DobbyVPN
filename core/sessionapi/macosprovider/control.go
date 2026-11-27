// Package macosprovider connects the desktop session owner to the Go runtime
// inside the installed macOS packet-tunnel system extension.
package macosprovider

import (
	"encoding/gob"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"core/iprange"
	"core/protocol"
	"core/sessionapi"
)

const Directory = "/Library/Application Support/DobbyVPN/Protection"

func init() {
	// The normalized Xray and TrustTunnel configurations have nested JSON
	// values. Gob retains the Go number types the native protocol expects.
	gob.Register(map[string]any{})
	gob.Register([]any{})
}

func socketPath(key string) (string, error) {
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 16 || key != strings.ToLower(key) {
		return "", errors.New("invalid provider connection identity")
	}
	return filepath.Join(Directory, key+".sock"), nil
}

type operation struct {
	Action   string
	Ref      sessionapi.SessionRef
	Instance string
	Summary  sessionapi.ProfileSummary
	Config   protocol.Config
	Excluded []iprange.Range
	Hosts    []string
	Reason   string
}
