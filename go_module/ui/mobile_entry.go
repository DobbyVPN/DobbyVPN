//go:build android || ios

package ui

// NewMobileClient constructs the shared client around the platform's narrow
// native transport. The UI never owns platform session or sharing policy.
func NewMobileClient() *MobileClient {
	return NewMobileClientWithTransport(newMobileAPI())
}

// NewMobileLogExporter returns the platform's existing explicit share adapter
// when one is available. A nil result leaves the shared action disabled rather
// than making the UI invent a second native sharing implementation.
func NewMobileLogExporter() LogExporter { return newMobileLogExporter() }
