//go:build windows

package diagnostics

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
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

func checkStderrOwnership(t *testing.T) {
	t.Helper()
	owned := map[uintptr]bool{os.Stderr.Fd(): true, stderrCapture.file.Fd(): true}
	for _, dll := range stderrCRTs {
		handle, _, _ := dll.NewProc("_get_osfhandle").Call(2)
		if owned[handle] {
			t.Fatalf("%s shares stderr handle %x", dll.Name, handle)
		}
		owned[handle] = true
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(windows.Handle(handle), &info); err != nil {
			t.Fatalf("%s lost stderr handle: %v", dll.Name, err)
		}
	}
	// Capture must not invalidate Go's inherited stderr or leave it protected.
	var flags uint32
	ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation").Call(os.Stderr.Fd(), uintptr(unsafe.Pointer(&flags)))
	if ok == 0 || flags&0x2 != 0 {
		t.Fatalf("inherited stderr handle: flags=%x error=%v", flags, err)
	}
}

// Exercise capture while attached to a debugger: Windows raises an exception
// when the CRT tries to close the inherited, protected handle. Forward it as a
// debugger normally would, so product exception handling must preserve startup.
func runStderrChild(t *testing.T, child *exec.Cmd) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	child.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x2} // DEBUG_ONLY_THIS_PROCESS
	if err := child.Start(); err != nil {
		return nil, err
	}
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	wait := kernel.NewProc("WaitForDebugEvent")
	resume := kernel.NewProc("ContinueDebugEvent")
	var debugErr error
	for {
		// DEBUG_EVENT: three DWORDs, padding to pointer alignment, then its union.
		var event struct {
			code, process, thread uint32
			data                  [20]uint64
		}
		ok, _, err := wait.Call(uintptr(unsafe.Pointer(&event)), 10000)
		if ok == 0 {
			debugErr = fmt.Errorf("wait for stderr debug event: %w", err)
			break
		}
		status := uintptr(0x10002) // DBG_CONTINUE
		if event.code == 1 && uint32(event.data[0]) != 0x80000003 {
			status = 0x80010001 // DBG_EXCEPTION_NOT_HANDLED, including protected closes.
		}
		if (event.code == 3 || event.code == 6) && event.data[0] != 0 {
			if err := windows.CloseHandle(windows.Handle(event.data[0])); err != nil {
				debugErr = errors.Join(debugErr, err)
			}
		}
		ok, _, err = resume.Call(uintptr(event.process), uintptr(event.thread), status)
		if ok == 0 {
			debugErr = errors.Join(debugErr, fmt.Errorf("continue stderr debug event: %w", err))
			break
		}
		if event.code == 5 {
			break
		}
	}
	if debugErr != nil {
		debugErr = errors.Join(debugErr, child.Process.Kill())
		if ok, _, err := kernel.NewProc("DebugActiveProcessStop").Call(uintptr(child.Process.Pid)); ok == 0 {
			debugErr = errors.Join(debugErr, fmt.Errorf("detach stderr debugger: %w", err))
		}
		t.Errorf("stderr debugger failed: %v", debugErr)
	}
	waitErr := child.Wait()
	return output.Bytes(), errors.Join(debugErr, waitErr)
}
