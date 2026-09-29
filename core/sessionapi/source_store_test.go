package sessionapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSourceStoreLoadTreatsMissingDirectoryAsEmpty(t *testing.T) {
	root := t.TempDir()
	store := FileSourceStore{
		Path: filepath.Join(root, ".dobbyvpn", "configs", "connection-url.txt"),
	}
	got, err := store.Load(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("Load() = %q, %v", got, err)
	}
}

func TestFileSourceStoreSavesAndClearsURL(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, ".dobbyvpn", "configs", "connection-url.txt")
	want := []byte("https://configs.invalid/saved")
	store := FileSourceStore{Path: current}
	if err := store.Save(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(context.Background())
	if err != nil || string(got) != string(want) {
		t.Fatalf("Load() = %q, %v", got, err)
	}
	info, err := os.Stat(current)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("saved URL mode = %v, %v", info, err)
	}
	if err := store.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(context.Background()); err != nil {
		t.Fatalf("empty store load = %v", err)
	}
}

func TestFileSourceStoreRejectsSymlinkedPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	store := FileSourceStore{Path: filepath.Join(link, "configs", "connection-url.txt")}
	if err := store.Save(context.Background(), []byte("https://configs.invalid/profile")); err == nil {
		t.Fatal("Save accepted a symlinked parent path")
	}
	if _, err := os.Stat(filepath.Join(target, "configs")); !os.IsNotExist(err) {
		t.Fatalf("Save created data through a symlink: %v", err)
	}
}
