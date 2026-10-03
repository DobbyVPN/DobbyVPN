//go:build windows

package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var stderrCRTs = []*windows.LazyDLL{
	windows.NewLazySystemDLL("msvcrt.dll"),
	windows.NewLazySystemDLL("ucrtbase.dll"),
}

var protectedStderrThread atomic.Uint32
var protectStderrHandler = syscall.NewCallback(func(pointers unsafe.Pointer) uintptr {
	// EXCEPTION_POINTERS starts with EXCEPTION_RECORD*, whose first DWORD is
	// ExceptionCode. Touch no Go locks or allocating code in this callback.
	record := *(*unsafe.Pointer)(pointers)
	if *(*uint32)(record) == 0xc0000235 && windows.GetCurrentThreadId() == protectedStderrThread.Load() {
		return ^uintptr(0) // EXCEPTION_CONTINUE_EXECUTION: CloseHandle returns failure.
	}
	return 0 // EXCEPTION_CONTINUE_SEARCH
})

var stderrRedirected bool // CaptureStderr and rotation serialize this operation.

func redirectStderr(file *os.File) (resultErr error) {
	// Native libraries use either the MSVC Universal CRT or MinGW's MSVCRT.
	// SetStdHandle alone does not replace their cached descriptor 2.
	// Load both before changing a standard handle: a newly loaded CRT would
	// otherwise adopt the other CRT's already redirected, independently owned FD.
	for _, dll := range stderrCRTs {
		if err := dll.Load(); err != nil {
			return err
		}
	}
	if !stderrRedirected {
		original, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
		if err != nil {
			return fmt.Errorf("read inherited stderr handle: %w", err)
		}
		if original != 0 && original != windows.InvalidHandle {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			// A debugger turns a refused close into STATUS_HANDLE_NOT_CLOSABLE.
			// Handle only this expected status on the initializing thread, and remove
			// the handler as soon as the CRTs own independent descriptors.
			kernel := windows.NewLazySystemDLL("kernel32.dll")
			protectedStderrThread.Store(windows.GetCurrentThreadId())
			defer protectedStderrThread.Store(0)
			// Windows runs continuation handlers even after a handled exception;
			// the Go runtime otherwise treats this foreign exception as fatal.
			for _, kind := range []string{"Exception", "Continue"} {
				addHandler := kernel.NewProc("AddVectored" + kind + "Handler")
				removeHandler := kernel.NewProc("RemoveVectored" + kind + "Handler")
				if err := removeHandler.Find(); err != nil {
					return err
				}
				handler, _, err := addHandler.Call(1, protectStderrHandler)
				if handler == 0 {
					return fmt.Errorf("protect stderr ownership during CRT initialization: %w", err)
				}
				defer func() {
					if removed, _, err := removeHandler.Call(handler); removed == 0 {
						resultErr = errors.Join(resultErr, fmt.Errorf("remove stderr ownership handler: %w", err))
					}
				}()
			}

			// Both CRTs and Go initially reference the same inherited HANDLE.
			// Keep it alive while _dup2 gives each CRT an independent handle;
			// otherwise the second CRT can close an unrelated recycled handle.
			const protectFromClose = 0x2
			var flags uint32
			getFlags := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
			if ok, _, err := getFlags.Call(uintptr(original), uintptr(unsafe.Pointer(&flags))); ok == 0 {
				return fmt.Errorf("read inherited stderr handle flags: %w", err)
			}
			if err := windows.SetHandleInformation(original, protectFromClose, protectFromClose); err != nil {
				return fmt.Errorf("protect inherited stderr handle: %w", err)
			}
			defer func() {
				if err := windows.SetHandleInformation(original, protectFromClose, flags&protectFromClose); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("restore inherited stderr handle flags: %w", err))
				}
			}()
		}
	}
	for _, dll := range stderrCRTs {
		if err := redirectCRTStderr(dll, windows.Handle(file.Fd())); err != nil {
			return err
		}
	}
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(file.Fd())); err != nil {
		return fmt.Errorf("replace Windows stderr handle: %w", err)
	}
	// Go's runtime resolves GetStdHandle on every panic write. SetCrashOutput
	// would write each panic fragment a second time into this same file.
	stderrRedirected = true
	return nil
}

// _dup2 serializes against CRT writes and owns a duplicate of the file handle.
// Keep errno on the same OS thread as the operation that produced it.
func redirectCRTStderr(dll *windows.LazyDLL, handle windows.Handle) error {
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
		return fmt.Errorf("%s %s: errno=%d", dll.Name, operation, errno)
	}
	var owned windows.Handle
	process := windows.CurrentProcess()
	if err := windows.DuplicateHandle(process, handle, process, &owned, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return fmt.Errorf("duplicate stderr handle for %s: %w", dll.Name, err)
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
