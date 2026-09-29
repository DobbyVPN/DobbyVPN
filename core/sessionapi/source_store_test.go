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
		Path:   filepath.Join(root, ".dobbyvpn", "configs", "connection-url.txt"),
		Legacy: filepath.Join(root, ".myapp", "configs", "connection-url.txt"),
	}
	got, err := store.Load(context.Background())
	if err != nil || len(got) != 0 {
		t.Fatalf("Load() = %q, %v", got, err)
	}
}

func TestFileSourceStoreMigratesLegacyURLOnce(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, ".dobbyvpn", "configs", "connection-url.txt")
	legacy := filepath.Join(root, ".myapp", "configs", "connection-url.txt")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	want := []byte("https://configs.invalid/saved")
	if err := os.WriteFile(legacy, want, 0600); err != nil {
		t.Fatal(err)
	}
	store := FileSourceStore{Path: current, Legacy: legacy}
	got, err := store.Load(context.Background())
	if err != nil || string(got) != string(want) {
		t.Fatalf("Load() = %q, %v", got, err)
	}
	if _, statErr := os.Stat(legacy); !os.IsNotExist(statErr) {
		t.Fatalf("legacy URL remains after migration: %v", statErr)
	}
	info, err := os.Stat(current)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("migrated URL mode = %v, %v", info, err)
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
