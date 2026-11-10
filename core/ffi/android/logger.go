//go:build android

package dobbyvpn

import (
	"path/filepath"
	"sync"

	"core/diagnostics"
	"core/log"
)

var (
	loggerInitMu sync.Mutex
	loggerReady  bool
)

func InitLogger(path string) bool {
	return initializeLogger(path) == ""
}

func initializeLogger(path string) string {
	loggerInitMu.Lock()
	defer loggerInitMu.Unlock()
	if loggerReady {
		return ""
	}
	if err := diagnostics.MigrateLegacy(path, []diagnostics.LegacySource{{Path: path + ".stderr", Label: "stderr"}, {Path: filepath.Join(filepath.Dir(path), "native_logs.jsonl"), Label: "android-native"}}); err != nil {
		return err.Error()
	}
	if err := diagnostics.CaptureStderr(path, path); err != nil {
		return err.Error()
	}
	if err := log.SetAppenderPath(path); err != nil {
		return err.Error()
	}
	diagnostics.SetRole(diagnostics.RoleAndroid)
	loggerReady = true
	log.Infof("android_exports", "Go app logger initialized")
	return ""
}

// AppendAndroidRecord shares the stderr owner's generation lock with native producers.
func AppendAndroidRecord(path, record string) error {
	writer, err := diagnostics.OpenAppender(path)
	if err != nil {
		return err
	}
	defer writer.Close()
	_, err = writer.Write([]byte(record))
	return err
}
