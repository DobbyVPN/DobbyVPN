package protected_dialer

import "syscall"

const SO_NO_TC_NETPOLICY = 0x1101

// iOS 26 research: Try additional socket options
// IP_BOUND_IF may help bind to specific interface on iOS 26
const IP_BOUND_IF = 25

type iosProtector struct{}

func (i *iosProtector) Protect(fd uintptr, network string) error {
	// iOS 26: Try SO_NO_TC_NETPOLICY first (original approach)
	err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
	if err != nil {
		log.Infof("[iOS-Protect] SO_NO_TC_NETPOLICY failed: %v", err)
	}
	
	// iOS 26 research: Try IP_BOUND_IF to bind to default interface
	// This might help with routing issues on iOS 26
	// Note: This may not work on all iOS versions, but let's try
	boundIfErr := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, 0)
	if boundIfErr != nil {
		// Not fatal - just log for research
		log.Infof("[iOS-Protect] IP_BOUND_IF not available: %v (this is normal on some iOS versions)", boundIfErr)
	}
	
	return nil
}

func init() {
	protector = &iosProtector{}
	log.Infof("[iOS-Protect] Initialized with iOS 26 research options (SO_NO_TC_NETPOLICY + IP_BOUND_IF research)")
}
