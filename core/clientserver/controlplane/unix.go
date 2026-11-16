//go:build !(windows || android || ios)

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var errWrongLocalConnection = errors.New("control plane accepted a non-Unix connection")

// configuredPeerUID reports the peer UID pinned by the service environment.
func configuredPeerUID() (uid int, pinned bool, err error) {
	value := os.Getenv("DOBBYVPN_CONTROL_PEER_UID")
	if value == "" {
		return 0, false, nil
	}
	uid, parseErr := strconv.Atoi(value)
	if parseErr != nil || uid < 0 {
		return 0, true, fmt.Errorf("DOBBYVPN_CONTROL_PEER_UID is invalid")
	}
	return uid, true, nil
}

// privilegedPeerUID resolves the peer UID of a privileged service, honoring a
// pinned environment first.
func privilegedPeerUID() (int, error) {
	uid, pinned, err := configuredPeerUID()
	if err != nil {
		return 0, err
	}
	if pinned {
		return uid, nil
	}
	uid, err = privilegedDefaultPeerUID()
	if err != nil {
		return 0, fmt.Errorf("DOBBYVPN_CONTROL_PEER_UID is required for a privileged control service: %w", err)
	}
	return uid, nil
}

// serviceStateUID is the account whose home holds the service's saved source:
// the pinned peer when the environment sets one, otherwise the account running
// the service. A headless service keeps its state without a desktop session.
func serviceStateUID() (int, error) {
	uid, pinned, err := configuredPeerUID()
	if err != nil {
		return 0, err
	}
	if pinned {
		return uid, nil
	}
	return currentUID(), nil
}

// authorizePeer decides per connection which local peer may drive the service.
// A privileged service keeps serving without a logged-in desktop UID, so a
// headless machine never depends on a session.
func authorizePeer(uid int) error {
	expected, pinned, err := configuredPeerUID()
	if err != nil {
		return err
	}
	if pinned {
		if uid != expected {
			return fmt.Errorf("control peer UID is not the installed user")
		}
		return nil
	}
	if currentUID() != 0 {
		if uid != currentUID() {
			return fmt.Errorf("control peer UID is not the service user")
		}
		return nil
	}
	return authorizePrivilegedPeer(uid)
}

var lookupUserID = user.LookupId // test seam for authorizeRegularUser

// authorizeRegularUser admits root and resolvable regular accounts (UID in
// [1000, 60000)) so a headless service serves the machine's CLI users without
// pinning any session.
func authorizeRegularUser(uid int, lookupID func(string) (*user.User, error)) error {
	if uid == 0 {
		return nil
	}
	if uid < 1000 || uid >= 60000 {
		return fmt.Errorf("control peer UID %d is not a regular user", uid)
	}
	if _, err := lookupID(strconv.Itoa(uid)); err != nil {
		return fmt.Errorf("control peer UID %d is not a local account", uid)
	}
	return nil
}

func ControlSocketPath() (string, error) {
	return controlSocketPathForOS(runtime.GOOS)
}

func controlSocketPathForOS(goos string) (string, error) {
	if path := os.Getenv("DOBBYVPN_CONTROL_SOCKET"); path != "" {
		return path, nil
	}
	if goos == "darwin" {
		return "/var/run/dobbyvpn/control.sock", nil
	}
	if goos == "linux" {
		// A system runtime directory survives session logins and logouts; the
		// packaged service declares it with RuntimeDirectory=dobbyvpn.
		return "/run/dobbyvpn/control.sock", nil
	}
	if currentUID() == 0 {
		uid, err := privilegedPeerUID()
		if err != nil {
			return "", err
		}
		runtimeDir := filepath.Join(string(filepath.Separator), "run", "user", strconv.Itoa(uid))
		if info, statErr := os.Stat(runtimeDir); statErr == nil && info.IsDir() {
			return filepath.Join(runtimeDir, "DobbyVPN", "control.sock"), nil
		}
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		var err error
		dir, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "DobbyVPN", "control.sock"), nil
}

func supervisedUnprivilegedSocket(path string, expected int) (bool, error) {
	if os.Getenv("DOBBYVPN_SUPERVISED_REQUEST") != "1" || currentUID() == 0 || expected == currentUID() {
		return false, nil
	}
	root := os.Getenv("DOBBYVPN_REQUEST_ROOT")
	if root == "" || !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false, fmt.Errorf("supervised control socket paths are invalid")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false, fmt.Errorf("supervised request root is unavailable: %w", err)
	}
	parent := filepath.Dir(path)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return false, fmt.Errorf("supervised control socket parent is unavailable: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedParent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false, fmt.Errorf("supervised control socket escapes its request")
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("supervised control socket parent is unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != currentUID() {
		return false, fmt.Errorf("supervised control socket parent has the wrong owner")
	}
	return true, nil
}

func ListenControlSocket() (net.Listener, error) {
	path, err := ControlSocketPath()
	if err != nil {
		return nil, err
	}
	listener, err := newRebindListener(path, listenControlSocket)
	if err != nil {
		return nil, err
	}
	return listener, nil
}

func DialDesktopControl(ctx context.Context) (net.Conn, error) {
	path, err := ControlSocketPath()
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

func AuthenticateLocalPeer(conn net.Conn) error {
	uid, err := peerUID(conn)
	if err != nil {
		return err
	}
	return authorizePeer(uid)
}

// listenOwnerUID is the chown target for the control socket, or -1 for the
// root-owned, world-connectable policy of a headless Linux service.
func listenOwnerUID() (int, error) {
	uid, pinned, err := configuredPeerUID()
	if err != nil {
		return 0, err
	}
	if pinned {
		return uid, nil
	}
	if currentUID() != 0 {
		return currentUID(), nil
	}
	return privilegedListenOwnerUID()
}

// socketModes reports the directory and socket modes for an ownership plan.
func socketModes(ownerUID int, supervisedUnprivileged bool) (os.FileMode, os.FileMode) {
	if supervisedUnprivileged {
		return 0o711, 0o622
	}
	if ownerUID == -1 {
		return privilegedSocketModes()
	}
	if ownerUID != currentUID() {
		return 0o711, 0o600
	}
	return 0o700, 0o600
}

func listenControlSocket(path string) (net.Listener, error) {
	ownerUID, ownerErr := listenOwnerUID()
	if ownerErr != nil {
		return nil, ownerErr
	}
	if directoryErr := ensureControlSocketDirectory(path); directoryErr != nil {
		return nil, directoryErr
	}
	supervisedUnprivileged, supervisedErr := supervisedUnprivilegedSocket(path, ownerUID)
	if supervisedErr != nil {
		return nil, supervisedErr
	}
	if permissionErr := setControlSocketDirectoryPermissions(path, ownerUID, supervisedUnprivileged); permissionErr != nil {
		return nil, permissionErr
	}
	if cleanupErr := removeStaleControlSocket(path); cleanupErr != nil {
		return nil, cleanupErr
	}
	var listenConfig net.ListenConfig
	lis, listenErr := listenConfig.Listen(context.Background(), "unix", path)
	if listenErr != nil {
		return nil, listenErr
	}
	// Removal is decided by removeUnservedControlSocket, never by Close, so a
	// listener that lost its path cannot unlink a replacement socket.
	if unixListener, ok := lis.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	if permissionErr := setControlSocketPermissions(path, ownerUID, supervisedUnprivileged); permissionErr != nil {
		return nil, errors.Join(permissionErr, lis.Close(), os.Remove(path))
	}
	return &removingUnixListener{Listener: lis, path: path}, nil
}
func ensureControlSocketDirectory(path string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	dirInfo, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect control socket parent: %w", err)
	}
	if !dirInfo.IsDir() {
		return fmt.Errorf("control socket parent is not a directory")
	}
	return nil
}

func setControlSocketDirectoryPermissions(path string, ownerUID int, supervisedUnprivileged bool) error {
	directory := filepath.Dir(path)
	if ownerUID != currentUID() && !supervisedUnprivileged {
		// Keep the directory root-owned so the desktop user cannot replace the
		// socket. Execute-only access permits connecting to the user-owned socket.
		if err := os.Chown(directory, currentUID(), -1); err != nil {
			return err
		}
	}
	dirMode, _ := socketModes(ownerUID, supervisedUnprivileged)
	return os.Chmod(directory, dirMode)
}

func removeStaleControlSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("control socket path is not a socket")
	}
	return os.Remove(path)
}

func setControlSocketPermissions(path string, ownerUID int, supervisedUnprivileged bool) error {
	_, socketMode := socketModes(ownerUID, supervisedUnprivileged)
	if err := os.Chmod(path, socketMode); err != nil {
		return err
	}
	if ownerUID >= 0 && ownerUID != currentUID() && !supervisedUnprivileged {
		if err := os.Chown(path, ownerUID, -1); err != nil {
			return err
		}
	}
	return nil
}

type removingUnixListener struct {
	net.Listener
	path string
	once sync.Once
}

// Close removes the socket file only when nothing serves it any more, so a
// replacement listener that took over the path keeps its socket.
func (l *removingUnixListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { removeUnservedControlSocket(l.path) })
	return err
}

// removeUnservedControlSocket removes a leftover socket file. A live listener
// accepts the probe connection, and anything that is not a socket is untouched.
func removeUnservedControlSocket(path string) {
	info, statErr := os.Lstat(path)
	if statErr != nil || info.Mode()&os.ModeSocket == 0 {
		return
	}
	probe, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
	if dialErr == nil {
		_ = probe.Close()
		return
	}
	_ = os.Remove(path)
}
