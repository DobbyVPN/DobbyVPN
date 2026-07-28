//go:build !(windows || android || ios)

package controlplane

import (
	"net"
	"os"
	"path/filepath"
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
