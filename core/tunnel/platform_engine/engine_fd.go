//go:build linux || android || ios

package platform_engine

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"

	"core/log"

	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
)

func startPlatformEngine(cfg EngineConfig) (bool, error) {
	offset := 0
	if runtime.GOOS == "ios" {
		offset = 4
	}
	return startStack(cfg, func() (device.Device, error) {
		if runtime.GOOS == "ios" {
			return openOwnedFileDevice(cfg.FD, TunnelMTU, offset)
		}
		return fdbased.Open(strconv.Itoa(cfg.FD), TunnelMTU, offset)
	})
}

// ownedFileDevice keeps the *os.File used as the iOS tunnel reader so teardown
// can close that file rather than only its integer descriptor. On iOS
// fdbased.Open wraps the descriptor with os.NewFile (fdbased/open_unix.go:15),
// which registers it with the runtime poller; fdbased.FD.Close then calls
// unix.Close on the integer only (fdbased/fd_unix.go:47), so the poller entry
// stays behind and iobased.dispatchLoop's os.File.Read never returns. gVisor
// Stack.Wait then waits forever on iobased.(*Endpoint).Wait (see the #272
// goroutine dump: stack.go:2073 -> endpoint.go:85 -> endpoint.go:99).
type ownedFileDevice struct {
	*iobased.Endpoint
	file *os.File
	name string
	once sync.Once
}

func openOwnedFileDevice(fd int, mtu uint32, offset int) (device.Device, error) {
	name := strconv.Itoa(fd)
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		return nil, fmt.Errorf("open packet-flow descriptor %d", fd)
	}
	endpoint, err := iobased.New(file, mtu, offset)
	if err != nil {
		return nil, fmt.Errorf("create packet endpoint: %w", err)
	}
	return &ownedFileDevice{Endpoint: endpoint, file: file, name: name}, nil
}

func (d *ownedFileDevice) Name() string { return d.name }
func (d *ownedFileDevice) Type() string { return fdbased.Driver }

// Close runs in the device-close phase, before stack-wait. Closing the os.File
// goes through internal/poll.FD.Close, which deregisters the descriptor from
// the runtime poller and wakes the blocked reader; closing the integer fd
// alone does not. The endpoint close then stops the outbound queue goroutine.
func (d *ownedFileDevice) Close() {
	d.once.Do(func() {
		_ = d.file.Close()
		d.Endpoint.Close()
	})
}

func stopPlatformEngine(ctx context.Context, stopDevice func(context.Context) error) error {
	// Linux's fd endpoint borrows the descriptor. Stop its packet writers and
	// interruptible readers before device.Close releases that descriptor for
	// reuse. iOS's owned file device instead closes its os.File in
	// device-close, which unblocks the endpoint reader before stack-wait.
	if (runtime.GOOS == "linux" || runtime.GOOS == "android") && ownedStack != nil {
		// Stack.Wait holds stack locks while removing NICs. Stop the FD reader
		// first, outside those locks: an ingress callback may need FindRoute
		// before it can return. The borrowed descriptor stays open until drain.
		if ownedDevice != nil {
			if err := stopPhaseCtx(ctx, "link-detach", func() { ownedDevice.Attach(nil) }); err != nil {
				return err
			}
		}
		if err := drainStack(ctx); err != nil {
			return err
		}
		log.Debugf(Category, "[Engine][FD] stack_drained=true device_release=starting")
	}
	return stopDevice(ctx)
}
func platformInterfaceName() string { return "" }
