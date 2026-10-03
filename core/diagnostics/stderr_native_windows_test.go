//go:build windows

package diagnostics

import (
	"fmt"
	"golang.org/x/sys/windows"
	"unsafe"
)

func writeNativeStderr(data []byte) error {
	for _, name := range []string{"msvcrt.dll", "ucrtbase.dll"} {
		proc := windows.NewLazySystemDLL(name).NewProc("_write")
		count, _, err := proc.Call(2, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
		if int32(count) != int32(len(data)) {
			return fmt.Errorf("%s stderr write: count=%d error=%v", name, int32(count), err)
		}
	}
	return nil
}
