//go:build android

package main

/*
extern int go_protect_socket(int fd);
*/
import "C"
import "go_module/tunnel/protected_dialer"

func init() {
	protected_dialer.MakeSocketProtected = func(fd uintptr) {
		C.go_protect_socket(C.int(fd))
	}
}
