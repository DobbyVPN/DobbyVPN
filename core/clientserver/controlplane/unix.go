//go:build !(windows || android || ios)

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var errWrongLocalConnection = errors.New("control plane accepted a non-Unix connection")

func expectedPeerUID() (int, error) {
	if value := os.Getenv("DOBBYVPN_CONTROL_PEER_UID"); value != "" {
		uid, err := strconv.Atoi(value)
		if err != nil || uid < 0 {
			return 0, fmt.Errorf("DOBBYVPN_CONTROL_PEER_UID is invalid")
		}
		return uid, nil
	}
	if uid := currentUID(); uid != 0 {
		return uid, nil
	}
	uid, err := privilegedDefaultPeerUID()
	if err != nil {
		return 0, fmt.Errorf("DOBBYVPN_CONTROL_PEER_UID is required for a privileged control service: %w", err)
	}
	return uid, nil
}

func ControlSocketPath() (string, error) {
	if path := os.Getenv("DOBBYVPN_CONTROL_SOCKET"); path != "" {
		return path, nil
	}
	if currentUID() == 0 {
		uid, err := expectedPeerUID()
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
	return listenControlSocket(path)
}

func DialDesktopControl(ctx context.Context) (net.Conn, error) {
	path, err := ControlSocketPath()
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", path)
}

func AuthenticateLocalPeer(conn net.Conn) error {
	expected, err := expectedPeerUID()
	if err != nil {
		return err
	}
	uid, err := peerUID(conn)
	if err != nil {
		return err
	}
	if uid != expected {
		return fmt.Errorf("control peer UID is not the installed user")
	}
	return nil
}

func listenControlSocket(path string) (net.Listener, error) {
	expected, expectedErr := expectedPeerUID()
	if expectedErr != nil {
		return nil, expectedErr
	}
	if directoryErr := ensureControlSocketDirectory(path); directoryErr != nil {
		return nil, directoryErr
	}
	supervisedUnprivileged, supervisedErr := supervisedUnprivilegedSocket(path, expected)
	if supervisedErr != nil {
		return nil, supervisedErr
	}
	if permissionErr := setControlSocketDirectoryPermissions(path, expected, supervisedUnprivileged); permissionErr != nil {
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
	if permissionErr := setControlSocketPermissions(path, expected, supervisedUnprivileged); permissionErr != nil {
		return nil, errors.Join(permissionErr, lis.Close())
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

func setControlSocketDirectoryPermissions(path string, expected int, supervisedUnprivileged bool) error {
	directory := filepath.Dir(path)
	if expected != currentUID() && !supervisedUnprivileged {
		// Keep the directory root-owned so the desktop user cannot replace the
		// socket. Execute-only access permits connecting to the user-owned socket.
		if err := os.Chown(directory, currentUID(), -1); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o700)
	if expected != currentUID() {
		mode = 0o711
	}
	return os.Chmod(directory, mode)
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

func setControlSocketPermissions(path string, expected int, supervisedUnprivileged bool) error {
	socketMode := os.FileMode(0o600)
	if supervisedUnprivileged {
		// The disposable runner is a different UID. Filesystem write permission
		// permits connect(2); Unix peer credentials still authenticate that one
		// exact UID before any RPC is accepted.
		socketMode = 0o622
	}
	if err := os.Chmod(path, socketMode); err != nil {
		return err
	}
	if expected != currentUID() && !supervisedUnprivileged {
		if err := os.Chown(path, expected, -1); err != nil {
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

func (l *removingUnixListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() { _ = os.Remove(l.path) })
	return err
}
