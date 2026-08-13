package exported_client

import (
	"bytes"
	"context"
	"encoding/base64"
	"net"
	"testing"
	"time"

	"github.com/cbeuw/Cloak/internal/client"
	"github.com/cbeuw/Cloak/internal/common"
	mux "github.com/cbeuw/Cloak/internal/multiplex"
)

type blockingDialer struct{ started chan struct{} }

func (d blockingDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	select {
	case d.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestNextClientSessionPreservesNormalProfileSettings(t *testing.T) {
	local := client.LocalConnConfig{MockDomainList: []string{"first.example", "second.example"}}
	remote := client.RemoteConnConfig{NumConn: 4}
	auth := client.AuthInfo{
		UID:        []byte("ordinary-user"),
		WorldState: common.WorldState{Rand: bytes.NewReader([]byte{1, 0, 0, 0, 7})},
	}

	gotRemote, gotAuth := nextClientSession(local, remote, auth)

	if gotRemote.NumConn != 4 {
		t.Fatalf("NumConn=%d, want configured value 4", gotRemote.NumConn)
	}
	if gotAuth.SessionId != 7 {
		t.Fatalf("SessionId=%d, want randomized fixture value 7", gotAuth.SessionId)
	}
	if gotAuth.MockDomain != "second.example" {
		t.Fatalf("MockDomain=%q, want second.example", gotAuth.MockDomain)
	}
	if !bytes.Equal(gotAuth.UID, auth.UID) {
		t.Fatal("ordinary profile UID changed")
	}
}

func TestNextClientSessionPreservesSingleplexProfile(t *testing.T) {
	local := client.LocalConnConfig{MockDomainList: []string{"only.example"}}
	remote := client.RemoteConnConfig{NumConn: 1, Singleplex: true}
	auth := client.AuthInfo{WorldState: common.WorldState{Rand: bytes.NewReader([]byte{0, 0, 0, 0, 9})}}

	gotRemote, gotAuth := nextClientSession(local, remote, auth)

	if gotRemote.NumConn != 1 || !gotRemote.Singleplex {
		t.Fatalf("singleplex settings changed: %#v", gotRemote)
	}
	if gotAuth.SessionId != 9 {
		t.Fatalf("SessionId=%d, want randomized fixture value 9", gotAuth.SessionId)
	}
}

func TestRegisterSessionClosesLateSessionAfterDisconnect(t *testing.T) {
	obfuscator, err := mux.MakeObfuscator(mux.EncryptionMethodPlain, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	session := mux.MakeSession(1, mux.SessionConfig{Obfuscator: obfuscator})
	client := &CkClient{}

	if got := client.registerSession(1, session); got != session {
		t.Fatal("registerSession replaced the session")
	}
	if !session.IsClosed() {
		t.Fatal("session created after disconnect remained open")
	}
	if len(client.sessions) != 0 {
		t.Fatal("session created after disconnect was published as active")
	}
}

func TestDisconnectClosesEveryRegisteredSession(t *testing.T) {
	first := testSession(t, 1)
	second := testSession(t, 2)
	client := &CkClient{connected: true, epoch: 1, sessions: make(map[*mux.Session]struct{})}
	client.registerSession(1, first)
	client.registerSession(1, second)

	if err := client.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if !first.IsClosed() || !second.IsClosed() {
		t.Fatalf("disconnect left sessions open: first=%t second=%t", first.IsClosed(), second.IsClosed())
	}
	if len(client.sessions) != 0 {
		t.Fatalf("disconnect retained %d sessions", len(client.sessions))
	}
}

func TestRegisterSessionRejectsPreviousConnectionEpoch(t *testing.T) {
	session := testSession(t, 1)
	client := &CkClient{connected: true, epoch: 2, sessions: make(map[*mux.Session]struct{})}

	client.registerSession(1, session)

	if !session.IsClosed() {
		t.Fatal("previous connection session remained open")
	}
	if len(client.sessions) != 0 {
		t.Fatal("previous connection session was registered in new epoch")
	}
}

func TestDisconnectCancelsBlockedSessionCreation(t *testing.T) {
	publicKey, err := base64.StdEncoding.DecodeString("IYoUzkle/T/kriE+Ufdm7AHQtIeGnBWbhhlTbmDpUUI=")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	client := NewCkClient(Config{
		ServerName: "example.com", ProxyMethod: "shadowsocks", EncryptionMethod: "plain",
		UID: []byte("ordinary-user"), PublicKey: publicKey, NumConn: 1,
		LocalHost: "127.0.0.1", LocalPort: "0", RemoteHost: "example.invalid", RemotePort: "443",
	})
	client.dialer = blockingDialer{started: started}
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	addr := client.listener.Addr().String()
	client.mu.Unlock()
	localConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer localConn.Close()
	if _, err := localConn.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session creation did not reach blocked dial")
	}
	if err := client.Disconnect(); err != nil {
		t.Fatal(err)
	}
}

func TestConnectRejectsUnfinishedPreviousRoutingGoroutine(t *testing.T) {
	done := make(chan struct{})
	client := &CkClient{routeDone: done}
	if err := client.Connect(); err == nil {
		t.Fatal("Connect accepted an unfinished previous routing goroutine")
	}
	close(done)
}

func TestRepeatedDisconnectWaitsForUnfinishedRoutingGoroutine(t *testing.T) {
	done := make(chan struct{})
	client := &CkClient{routeDone: done}
	finished := make(chan error, 1)
	go func() { finished <- client.Disconnect() }()
	time.Sleep(10 * time.Millisecond)
	close(done)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Disconnect did not wait for unfinished routing goroutine")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.routeDone != nil {
		t.Fatal("completed routing goroutine was not cleared")
	}
}

func testSession(t *testing.T, id uint32) *mux.Session {
	t.Helper()
	obfuscator, err := mux.MakeObfuscator(mux.EncryptionMethodPlain, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	return mux.MakeSession(id, mux.SessionConfig{Obfuscator: obfuscator})
}
