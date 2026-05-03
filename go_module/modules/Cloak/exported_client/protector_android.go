//go:build android
// +build android

package exported_client

/*
extern int go_protect_socket(int fd); // Import function from C/JNI layer
*/
import "C"
import (
	"fmt"
	"go_module/log"
	"syscall"
)

func protector(network string, address string, c syscall.RawConn) error {
	var protectErr error
	controlErr := c.Control(func(fd uintptr) {
		res := C.go_protect_socket(C.int(fd))
		if res != 1 {
			protectErr = fmt.Errorf("go_protect_socket(fd=%d) returned %d", fd, res)
			log.Errorf(Category, "Protect failed: %v for %s %s", protectErr, network, address)
		} else {
			log.Infof(Category, "Protect success: fd=%d", fd)
		}
	})
	if controlErr != nil {
		return controlErr
	}
	return protectErr
}
