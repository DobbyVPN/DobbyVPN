//go:build windows

package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"unsafe"

	"golang.org/x/sys/windows"
)

func redirectStderr(file *os.File) error {
	// Native libraries use either the MSVC Universal CRT or MinGW's MSVCRT.
	// SetStdHandle alone does not replace their cached descriptor 2.
	for _, name := range []string{"msvcrt.dll", "ucrtbase.dll"} {
		if err := redirectCRTStderr(name, windows.Handle(file.Fd())); err != nil {
			return err
		}
	}
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(file.Fd())); err != nil {
		return err
	}
	// The Go runtime may have cached the startup service handle. CrashOutput
	// explicitly follows the retained file after every descriptor rotation.
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		return err
	}
	return nil
}

// _dup2 serializes against CRT writes and owns a duplicate of the file handle.
// Keep errno on the same OS thread as the operation that produced it.
func redirectCRTStderr(name string, handle windows.Handle) error {
	dll := windows.NewLazySystemDLL(name)
	open := dll.NewProc("_open_osfhandle")
	duplicate := dll.NewProc("_dup2")
	closeFD := dll.NewProc("_close")
	getErrno := dll.NewProc("_get_errno")
	for _, proc := range []*windows.LazyProc{open, duplicate, closeFD, getErrno} {
		if err := proc.Find(); err != nil {
			return err
		}
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	crtError := func(operation string) error {
		var errno int32
		getErrno.Call(uintptr(unsafe.Pointer(&errno)))
		return fmt.Errorf("%s %s: errno=%d", name, operation, errno)
	}
	var owned windows.Handle
	process := windows.CurrentProcess()
	if err := windows.DuplicateHandle(process, handle, process, &owned, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return err
	}
	// _O_WRONLY | _O_BINARY: preserve the exact bytes emitted by the library.
	fd, _, _ := open.Call(uintptr(owned), 0x0001|0x8000)
	if int32(fd) == -1 {
		return errors.Join(crtError("_open_osfhandle"), windows.CloseHandle(owned))
	}
	// An unattached GUI/service CRT may allocate descriptor 2 itself.
	if fd == 2 {
		return nil
	}
	result, _, _ := duplicate.Call(fd, 2)
	var err error
	if int32(result) == -1 {
		err = crtError("_dup2")
	}
	result, _, _ = closeFD.Call(fd)
	if int32(result) == -1 {
		err = errors.Join(err, crtError("_close"))
	}
	return err
}
