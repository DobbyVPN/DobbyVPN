//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLocalLogDirectoryDescriptorSurvivesParentReplacement(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, ".dobbyvpn")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, _, err := openPinnedLocalLogBase(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()

	retained := filepath.Join(root, "retained-parent")
	if err := os.Rename(parent, retained); err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(root, "redirect-parent")
	if err := os.Mkdir(redirect, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(redirect, parent); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	fd, err := unix.Openat(int(directory.Fd()), "app_logs.txt", unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "app_logs.txt")
	if file == nil {
		_ = unix.Close(fd)
		t.Fatal("Openat returned an invalid descriptor")
	}
	if _, err := file.WriteString("descriptor-owned\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(retained, "app_logs.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "descriptor-owned\n" {
		t.Fatalf("descriptor target = %q", data)
	}
	if _, err := os.Lstat(filepath.Join(redirect, "app_logs.txt")); !os.IsNotExist(err) {
		t.Fatalf("replacement parent received the write: %v", err)
	}
}

func TestClearLocalLogAtBaseRejectsPathOutsidePinnedBase(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "trusted")
	outside := filepath.Join(root, "outside", "app_logs.txt")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(outside), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := clearLocalLogFileAtBase(outside, base); err == nil {
		t.Fatal("outside-base log path unexpectedly accepted")
	}
	if _, err := os.Lstat(filepath.Dir(outside)); err != nil {
		t.Fatalf("outside path was unexpectedly mutated: %v", err)
	}
}

func TestClearLocalLogAtBaseUsesSuppliedBase(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	if err := os.Mkdir(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(first, ".dobbyvpn", "app_logs.txt")
	if err := clearLocalLogFileAtBase(path, second); err == nil {
		t.Fatal("path accepted with an unrelated pinned base")
	}
	if _, err := os.Lstat(filepath.Join(first, ".dobbyvpn")); !os.IsNotExist(err) {
		t.Fatalf("unrelated base caused mutation: %v", err)
	}
}

func TestClearLocalLogAtBaseRejectsRelativeBaseBeforeMutation(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "trusted")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, ".dobbyvpn", "app_logs.txt")
	if err := clearLocalLogFileAtBase(path, "."); err == nil {
		t.Fatal("relative base unexpectedly accepted")
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("relative-base rejection mutated the path: %v", err)
	}
}

func TestClearLocalLogAtBaseRejectsReplacedBaseBeforeMutation(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "trusted")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	replaced := filepath.Join(root, "replaced")
	if err := os.Rename(base, replaced); err != nil {
		t.Fatal(err)
	}
	redirect := filepath.Join(root, "redirect")
	if err := os.Mkdir(redirect, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(redirect, base); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := filepath.Join(base, ".dobbyvpn", "app_logs.txt")
	if err := clearLocalLogFileAtBase(path, base); err == nil {
		t.Fatal("replaced base unexpectedly accepted")
	}
	if _, err := os.Lstat(filepath.Join(redirect, ".dobbyvpn")); !os.IsNotExist(err) {
		t.Fatalf("replacement base received a mutation: %v", err)
	}
}

func TestClearLocalLogAtBaseRejectsSymlinkedBaseAncestor(t *testing.T) {
	root := t.TempDir()
	realRoot := filepath.Join(root, "real-root")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasRoot := filepath.Join(root, "alias-root")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	base := filepath.Join(aliasRoot, "trusted")
	if err := os.Mkdir(filepath.Join(realRoot, "trusted"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, ".dobbyvpn", "app_logs.txt")
	if err := clearLocalLogFileAtBase(path, base); err == nil {
		t.Fatal("symlinked base ancestor unexpectedly accepted")
	}
	if _, err := os.Lstat(filepath.Join(realRoot, "trusted", ".dobbyvpn")); !os.IsNotExist(err) {
		t.Fatalf("symlinked base ancestor received a mutation: %v", err)
	}
}

func TestClearLocalLogAtBaseRejectsWritableBaseBeforeCreation(t *testing.T) {
	base := t.TempDir()
	if err := os.Chmod(base, 0o770); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, ".dobbyvpn", "app_logs.txt")
	if err := clearLocalLogFileAtBase(path, base); err == nil {
		t.Fatal("group-writable base unexpectedly accepted")
	}
	if _, err := os.Lstat(filepath.Join(base, ".dobbyvpn")); !os.IsNotExist(err) {
		t.Fatalf("writable base was mutated before rejection: %v", err)
	}
}
