package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// The native builders set these from VERSION, the selected revision and build
// configuration. Plain go build retains an explicit development identity.
var Version = "development"
var Commit = ""
var Configuration = "development"

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && Commit == "" {
				Commit = setting.Value
			}
		}
	}
}

func Fields() map[string]any {
	return map[string]any{
		"version": Version, "commit": Commit, "configuration": Configuration,
		"platform": runtime.GOOS, "architecture": runtime.GOARCH, "go_version": runtime.Version(),
	}
}
