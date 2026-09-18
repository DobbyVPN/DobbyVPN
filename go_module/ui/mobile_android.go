//go:build android

package ui

import (
	"fmt"

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
