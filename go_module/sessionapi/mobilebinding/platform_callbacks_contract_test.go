package mobilebinding

// Keep the public callback contract compile-checked on every host, including
// hosts that cannot build the mobile platform adapter itself.
type callbackContractProbe struct{}

func (callbackContractProbe) AcquireTunnel(string, int64) int32                                { return -1 }
func (callbackContractProbe) ReleaseTunnel(string, int64, int32) bool                          { return false }
func (callbackContractProbe) ProtectSocket(string, int64, int32) bool                          { return false }
func (callbackContractProbe) PublishState(string, int64, int64, string, int32, string, string) {}

var _ PlatformCallbacks = callbackContractProbe{}
