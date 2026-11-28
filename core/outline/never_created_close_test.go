package outline

import "testing"

// A device whose creation failed reaches the runtime as a nil device; closing
// it must succeed so Stop can release the VPN network.
func TestNeverCreatedDeviceClosesCleanly(t *testing.T) {
	var device *OutlineDevice
	if err := device.Close(); err != nil {
		t.Fatalf("closing a never-created device failed: %v", err)
	}
}
