package controlplane

import (
	"path/filepath"
	"testing"
)

func TestLoadOrCreateControlTokenIsStableAndOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "token")
	t.Setenv("DOBBYVPN_CONTROL_TOKEN_PATH", path)
	first, err := LoadOrCreateControlToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateControlToken()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != 64 {
		t.Fatal("unexpected token result")
	}
	assertOwnerOnlyTokenPermissions(t, path)
}
