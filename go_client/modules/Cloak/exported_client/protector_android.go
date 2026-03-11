//go:build android
// +build android

package exported_client

/*
extern int go_protect_socket(int fd); // Импортируем функцию из C/JNI слоя
*/
import "C"
import (
	"go_client/log"
	"syscall"
)

func protector(network string, address string, c syscall.RawConn) error {
	return c.Control(func(fd uintptr) {
		res := C.go_protect_socket(C.int(fd))
		if res != 1 {
			log.Infof("Protect failed: go_protect_socket(fd=%d) returned %d for %s %s", fd, res, network, address)
		} else {
			log.Infof("Protect success: fd=%d", fd)
		}
	})
}
