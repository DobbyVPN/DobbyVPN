//go:build linux

package tunnel

import (
	"errors"
	"os"
	"testing"

	"go_module/tunnel/platform_engine"

	"golang.org/x/sys/unix"
)

func TestFDEngineRejectsBusyBeforeDuplicatingDescriptor(t *testing.T) {
	engineMu.Lock()
	previous := activeEngine
	activeEngine = &Engine{}
	engineMu.Unlock()
	t.Cleanup(func() {
		engineMu.Lock()
		activeEngine = previous
		engineMu.Unlock()
	})

	_, err := StartOwnedFDEngine(platform_engine.EngineConfig{FD: -1})
	if !errors.Is(err, ErrEngineBusy) {
		t.Fatalf("StartOwnedFDEngine() error = %v, want ErrEngineBusy", err)
	}
}

func TestFDEngineClosesRejectedDuplicateAndRetainsOriginal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })

	want := errors.New("platform rejected descriptor")
	engineFD := -1
	_, err = startOwnedFDEngineLocked(
		platform_engine.EngineConfig{FD: int(reader.Fd())},
		func(cfg platform_engine.EngineConfig) (*Engine, bool, error) {
			engineFD = cfg.FD
			return nil, false, want
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("startOwnedFDEngineLocked() error=%v, want %v", err, want)
	}
	if engineFD < 0 || engineFD == int(reader.Fd()) {
		t.Fatalf("engine descriptor=%d, original=%d", engineFD, reader.Fd())
	}
	if _, err := writer.Write([]byte{'x'}); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if count, err := reader.Read(buffer); err != nil || count != 1 || buffer[0] != 'x' {
		t.Fatalf("original descriptor read count=%d value=%q err=%v", count, buffer, err)
	}
	if _, err := unix.FcntlInt(uintptr(engineFD), unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("rejected duplicate remains open: %v", err)
	}
}

func TestFDEngineDoesNotRecloseAcceptedDescriptor(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	t.Cleanup(func() { _ = writer.Close() })
	reuseSource, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reuseSource.Close() })

	want := errors.New("post-acceptance initialization failed")
	reusedFD := -1
	_, err = startOwnedFDEngineLocked(
		platform_engine.EngineConfig{FD: int(reader.Fd())},
		func(cfg platform_engine.EngineConfig) (*Engine, bool, error) {
			if closeErr := unix.Close(cfg.FD); closeErr != nil {
				t.Fatal(closeErr)
			}
			if dupErr := unix.Dup2(int(reuseSource.Fd()), cfg.FD); dupErr != nil {
				t.Fatal(dupErr)
			}
			reusedFD = cfg.FD
			return nil, true, want
		},
	)
	if !errors.Is(err, want) {
		t.Fatalf("startOwnedFDEngineLocked() error=%v, want %v", err, want)
	}
	t.Cleanup(func() { _ = unix.Close(reusedFD) })
	if _, err := unix.FcntlInt(uintptr(reusedFD), unix.F_GETFD, 0); err != nil {
		t.Fatalf("accepted descriptor number was closed again after reuse: %v", err)
	}
}
