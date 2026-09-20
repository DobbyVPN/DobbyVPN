//go:build android

package ui

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2/driver"
	dobbyvpn "go_module/android_exports"
)

func newMobileAPI() mobileAPI {
	transport := androidTransport{}
	return mobileAPI{
		configure: transport.Configure,
		start:     transport.Start,
		stop:      transport.Stop,
		snapshot:  transport.Snapshot,
		reset:     transport.Reset,
	}
}

type androidLogExporter struct{}

func newMobileLogExporter() LogExporter { return androidLogExporter{} }

func platformDiagnosticPaths() ([]string, error) {
	var encoded string
	err := driver.RunNative(func(value any) error {
		context, ok := value.(*driver.AndroidContext)
		if !ok {
			return fmt.Errorf("Fyne did not provide an Android native context")
		}
		dobbyvpn.SetAndroidContext(context.VM, context.Env, context.Ctx)
		encoded = dobbyvpn.DiagnosticPaths()
		if strings.TrimSpace(encoded) == "" {
			return fmt.Errorf("Android diagnostic bridge returned no paths")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths := strings.Split(encoded, "\n")
	for index := range paths {
		paths[index] = strings.TrimSpace(paths[index])
	}
	return paths, nil
}

func (androidLogExporter) Export(ctx context.Context, lines []string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	payload := []byte(strings.Join(lines, "\n"))
	var exported bool
	err := driver.RunNative(func(value any) error {
		context, ok := value.(*driver.AndroidContext)
		if !ok {
			return fmt.Errorf("Fyne did not provide an Android native context")
		}
		dobbyvpn.SetAndroidContext(context.VM, context.Env, context.Ctx)
		exported = dobbyvpn.ExportLogs(payload)
		return nil
	})
	if err != nil {
		return err
	}
	if !exported {
		return fmt.Errorf("Android log share could not be opened")
	}
	return nil
}

type androidTransport struct{}

func markNativeStartup()    {}
func markNativeUIAttached() {}

func (androidTransport) attach() error {
	return driver.RunNative(func(value any) error {
		context, ok := value.(*driver.AndroidContext)
		if !ok {
			return fmt.Errorf("Fyne did not provide an Android native context")
		}
		dobbyvpn.SetAndroidContext(context.VM, context.Env, context.Ctx)
		return nil
	})
}

func (a androidTransport) Configure(session string, sequence int64, raw []byte) string {
	if err := a.attach(); err != nil {
		return mobileFailure("PLATFORM_FAILED", err.Error())
	}
	return dobbyvpn.ConfigureSession(session, sequence, raw)
}

func (a androidTransport) Start(session string, sequence int64, mode string, index int32) string {
	if err := a.attach(); err != nil {
		return mobileFailure("PLATFORM_FAILED", err.Error())
	}
	switch dobbyvpn.PrepareAndroidService() {
	case 1:
		return dobbyvpn.StartSession(session, sequence, mode, index)
	case 0:
		return mobileFailure("PLATFORM_PERMISSION_REQUIRED", "Android VPN permission is required; approve it and press Connect again")
	default:
		return mobileFailure("PLATFORM_FAILED", "Android VPN service could not be prepared")
	}
}

func (a androidTransport) Stop(session string, generation int64) string {
	if err := a.attach(); err != nil {
		return mobileFailure("PLATFORM_FAILED", err.Error())
	}
	return dobbyvpn.StopSession(session, generation)
}

func (a androidTransport) Snapshot(session string) string {
	if err := a.attach(); err != nil {
		return mobileFailure("PLATFORM_FAILED", err.Error())
	}
	return dobbyvpn.SnapshotSession(session)
}

func (a androidTransport) Reset(session string, sequence int64) string {
	if err := a.attach(); err != nil {
		return mobileFailure("PLATFORM_FAILED", err.Error())
	}
	return dobbyvpn.ResetSession(session, sequence)
}

func mobileFailure(code, message string) string {
	return fmt.Sprintf(`{"ok":false,"error":{"code":%q,"message":%q}}`, code, message)
}
