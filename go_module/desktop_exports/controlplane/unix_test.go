//go:build !(windows || android || ios)

package controlplane

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestControlSocketIsOwnerOnlyAndPeerVerifiable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime", "control.sock")
	t.Setenv("DOBBYVPN_CONTROL_SOCKET", path)
	lis, err := ListenControlSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("socket mode = %o", info.Mode().Perm())
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := lis.Accept()
		if err == nil {
			_, err = peerUID(conn)
			_ = conn.Close()
		}
		accepted <- err
	}()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("same-user peer credential failed: %v", err)
	}
}

func TestSupervisedUnprivilegedSocketAuthenticatesAcrossUIDs(t *testing.T) {
	if currentUID() == 0 {
		t.Skip("supervised unprivileged socket requires a non-root service UID")
	}
	root := t.TempDir()
	parent := filepath.Join(root, ".dobbyvpn-run")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "s")
	t.Setenv("DOBBYVPN_CONTROL_SOCKET", path)
	t.Setenv("DOBBYVPN_CONTROL_PEER_UID", strconv.Itoa(currentUID()+1))
	t.Setenv("DOBBYVPN_SUPERVISED_REQUEST", "1")
	t.Setenv("DOBBYVPN_REQUEST_ROOT", root)

	lis, err := ListenControlSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0622 {
		t.Fatalf("supervised socket mode = %o", info.Mode().Perm())
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if parentInfo.Mode().Perm() != 0711 {
		t.Fatalf("supervised socket parent mode = %o", parentInfo.Mode().Perm())
	}
}

func TestSupervisedUnprivilegedSocketRejectsOutsideRequest(t *testing.T) {
	if currentUID() == 0 {
		t.Skip("supervised unprivileged socket requires a non-root service UID")
	}
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "s")
	t.Setenv("DOBBYVPN_CONTROL_SOCKET", path)
	t.Setenv("DOBBYVPN_CONTROL_PEER_UID", strconv.Itoa(currentUID()+1))
	t.Setenv("DOBBYVPN_SUPERVISED_REQUEST", "1")
	t.Setenv("DOBBYVPN_REQUEST_ROOT", root)

	lis, err := ListenControlSocket()
	if lis != nil {
		_ = lis.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "escapes its request") {
		t.Fatalf("outside supervised socket error = %v", err)
	}
}
