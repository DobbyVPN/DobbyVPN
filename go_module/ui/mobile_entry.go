//go:build android || ios

package ui

// NewMobileClient constructs the shared client around the platform's narrow
// native transport. The UI never calls a platform export directly.
func NewMobileClient() *MobileClient {
	return NewMobileClientWithTransport(newMobileAPI())
}
