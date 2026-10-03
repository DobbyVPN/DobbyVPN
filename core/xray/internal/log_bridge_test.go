package internal

import (
	"core/log"
	"path/filepath"
	"sync"
	"testing"

	xrayLog "github.com/xtls/xray-core/common/log"
)

func TestXrayLoggingLevelConcurrentWithMessages(t *testing.T) {
	if err := log.SetPath(filepath.Join(t.TempDir(), "xray.jsonl")); err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	SetupXrayLogging(xrayLog.Severity_Debug)
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 100 {
			SetupXrayLogging(xrayLog.Severity_Info)
			SetupXrayLogging(xrayLog.Severity_Debug)
		}
	})
	workers.Go(func() {
		for range 200 {
			registeredXrayBridge.Handle(&xrayLog.GeneralMessage{Severity: xrayLog.Severity_Debug, Content: "concurrent library message"})
		}
	})
	workers.Wait()
}
