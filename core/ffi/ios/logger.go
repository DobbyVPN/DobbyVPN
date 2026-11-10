//go:build ios

package dobbyvpn

import (
	"core/diagnostics"
	"core/log"
	"fmt"
	"path/filepath"
	"sync"
)

var loggerInitMu sync.Mutex
var loggerReady bool

func InitLogger(path string) bool { return InitLoggerError(path) == "" }

// InitLoggerError exposes startup failures before the diagnostic sink exists.
func InitLoggerError(path string) (result string) {
	result = "logger initialization panicked; see process diagnostics"
	defer guard("InitLoggerError")()
	loggerInitMu.Lock()
	defer loggerInitMu.Unlock()
	if loggerReady {
		return ""
	}
	sources := []diagnostics.LegacySource{{Path: path + ".stderr", Label: "stderr"}}
	switch filepath.Base(path) {
	case "go_app_logs.jsonl":
		sources = append(sources, diagnostics.LegacySource{Path: filepath.Join(filepath.Dir(path), "app_logs.txt"), Label: "app"}, diagnostics.LegacySource{Path: filepath.Join(filepath.Dir(path), "ui_diagnostics.jsonl"), Label: "frontend"})
	case "go_tunnel_logs.jsonl":
		sources = append(sources, diagnostics.LegacySource{Path: filepath.Join(filepath.Dir(path), "tunnel_native.jsonl"), Label: "tunnel"})
	}
	if err := diagnostics.MigrateLegacy(path, sources); err != nil {
		return fmt.Sprintf("migrate diagnostics: %v", err)
	}
	if err := diagnostics.CaptureStderr(path, path); err != nil {
		return fmt.Sprintf("capture stderr: %v", err)
	}
	if err := log.SetAppenderPath(path); err != nil {
		return fmt.Sprintf("initialize structured diagnostics: %v", err)
	}
	// The app and the tunnel extension share one App Group directory but write
	// different owned files; the role names which process produced a stage.
	if filepath.Base(path) == "go_tunnel_logs.jsonl" {
		diagnostics.SetRole(diagnostics.RoleIOSTunnel)
	} else {
		diagnostics.SetRole(diagnostics.RoleIOSApp)
	}
	loggerReady = true
	return ""
}
