package controljson

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"

	"go_module/sessionapi"
	"go_module/sessionapi/mobilebinding"
)

func TestSnapshotUsesSharedMobileJSONShape(t *testing.T) {
	client, server := net.Pipe()
	handler := Handler{Binding: mobilebinding.NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{}))}
	done := make(chan error, 1)
	go func() { done <- handler.ServeConn(server) }()

	if _, err := client.Write([]byte(`{"method":"Snapshot","params":{"session_id":""}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK {
		t.Fatalf("Snapshot response = %s", line)
	}
	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(response.Result, &snapshot); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"session_id", "sequence", "generation", "state", "source_kind", "profiles", "warnings", "recovering"} {
		if _, ok := snapshot[key]; !ok {
			t.Fatalf("shared snapshot is missing %q: %s", key, response.Result)
		}
	}
	_ = client.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUnknownMethodReturnsTypedFailure(t *testing.T) {
	client, server := net.Pipe()
	handler := Handler{Binding: mobilebinding.NewForTest(sessionapi.NewManager(sessionapi.ManagerOptions{}))}
	done := make(chan error, 1)
	go func() { done <- handler.ServeConn(server) }()

	if _, err := client.Write([]byte(`{"method":"Watch","params":{}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(client).ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("unknown command response = %s", line)
	}
	_ = client.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestReadLineBoundedRejectsOversizedRequest(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		_, _ = client.Write([]byte("123456\n"))
		_ = client.Close()
	}()
	_, err := readLineBounded(bufio.NewReader(server), 5)
	if err == nil {
		t.Fatal("oversized request was accepted")
	}
	_ = server.Close()
}
