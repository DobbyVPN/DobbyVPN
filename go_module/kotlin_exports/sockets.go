package main

/*
extern int go_protect_socket(int fd);
*/
import "C"
import "go_module/tunnel/protected_dialer"

func init() {
	protected_dialer.MakeSocketProtected = func(fd uintptr) bool {
		return C.go_protect_socket(C.int(fd)) == 1
	}
}
