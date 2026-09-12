//go:build !(windows || android || ios)

package controlplane

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc/credentials"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var errWrongLocalConnection = errors.New("control plane accepted a non-Unix connection")

type unixPeerAuthInfo struct {
	uid      int
	expected int
}

func (unixPeerAuthInfo) AuthType() string                 { return "dobby-unix-peer" }
func (a unixPeerAuthInfo) ControlPeerAuthenticated() bool { return a.uid == a.expected }

type UnixPeerCredentials struct{}

func (UnixPeerCredentials) ClientHandshake(context.Context, string, net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("Unix peer credentials are server-only")
}
func (UnixPeerCredentials) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	expected, err := expectedPeerUID()
	if err != nil {
		return nil, nil, err
	}
	uid, err := peerUID(conn)
	if err != nil {
		return nil, nil, err
	}
	if uid != expected {
		return nil, nil, fmt.Errorf("control peer UID is not the installed user")
	}
	return conn, unixPeerAuthInfo{uid: uid, expected: expected}, nil
}
func (UnixPeerCredentials) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "unix-peer"}
}
func (UnixPeerCredentials) Clone() credentials.TransportCredentials { return UnixPeerCredentials{} }
func (UnixPeerCredentials) OverrideServerName(string) error         { return nil }

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
		runtimeDir := filepath.Join("/run/user", strconv.Itoa(uid))
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
	expected, err := expectedPeerUID()
	if err != nil {
		return nil, err
	}
	path, err := ControlSocketPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	dirInfo, err := os.Lstat(filepath.Dir(path))
	if err != nil || !dirInfo.IsDir() {
		return nil, fmt.Errorf("control socket parent is not a directory")
	}
	supervisedUnprivileged, err := supervisedUnprivilegedSocket(path, expected)
	if err != nil {
		return nil, err
	}
	if expected != currentUID() {
		// A privileged daemon keeps the directory root-owned so the desktop
		// user cannot replace the socket. Execute-only access is sufficient to
		// connect to the user-owned 0600 socket below.
		if !supervisedUnprivileged {
			if err := os.Chown(filepath.Dir(path), currentUID(), -1); err != nil {
				return nil, err
			}
		}
		if err := os.Chmod(filepath.Dir(path), 0711); err != nil {
			return nil, err
		}
	} else if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("control socket path is not a socket")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lis, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	socketMode := os.FileMode(0600)
	if supervisedUnprivileged {
		// The disposable runner is a different UID. Filesystem write permission
		// permits connect(2); Unix peer credentials still authenticate that one
		// exact UID before any RPC is accepted.
		socketMode = 0622
	}
	if err := os.Chmod(path, socketMode); err != nil {
		_ = lis.Close()
		return nil, err
	}
	if expected != currentUID() && !supervisedUnprivileged {
		if err := os.Chown(path, expected, -1); err != nil {
			_ = lis.Close()
			return nil, err
		}
	}
	return &removingUnixListener{Listener: lis, path: path}, nil
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
