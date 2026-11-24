package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestConfigureReadsRootSettingsBeforeProfileTables(t *testing.T) {
	const profile = "# provider settings\r\nlogs_endpoint = \"https://logs.invalid/upload/\"\r\n\r\n[[Outline]]\r\nServer = \"vpn.invalid\"\r\nPort = 443\r\nPassword = \"example\"\r\n"
	original := configureStdin
	t.Cleanup(func() { configureStdin = original })
	configureStdin = strings.NewReader(profile)
	for _, source := range []string{"-", profile} {
		got, err := readSource(source)
		if err != nil || string(got) != profile {
			t.Fatalf("readSource lost inline profile: %v", err)
		}
	}
}

func TestConfigureKeepsSourceAndFilenameDistinct(t *testing.T) {
	t.Chdir(t.TempDir())
	const filename = "logs_endpoint=test"
	if err := os.WriteFile(filename, []byte("file contents"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readSource(filename)
	if err != nil || string(got) != "file contents" {
		t.Fatalf("existing filename: got %q, err %v", got, err)
	}
	if _, err = readStdinSource(strings.NewReader(filename), 1<<20); err == nil {
		t.Fatal("stdin accepted an assignment-shaped filename")
	}
	if _, err = readSource("missing=example.toml"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing assignment-shaped filename: %v", err)
	}
	const url = "https://subscription.invalid/sub?token=example"
	got, err = readStdinSource(strings.NewReader(url+"\n"), 1<<20)
	if err != nil || string(got) != url {
		t.Fatalf("stdin URL: got %q, err %v", got, err)
	}
	for _, invalid := range []string{"/dir/file=value", "http://subscription.invalid/sub?token=example", "subscription.invalid/sub?token=example"} {
		if _, err := readStdinSource(strings.NewReader(invalid), 1<<20); err == nil {
			t.Fatalf("accepted invalid stdin source %q", invalid)
		}
	}
}
