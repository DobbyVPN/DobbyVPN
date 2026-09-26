package protected_dialer

type iosProtector struct{}

// Network connections created by a NetworkExtension packet tunnel provider
// bypass that provider's own packet tunnel. iOS does not need the macOS-only
// socket option used by earlier versions; setting that option fails with EINVAL.
func (i *iosProtector) Protect(uintptr, string) error { return nil }

func init() {
	protector = &iosProtector{}
}
