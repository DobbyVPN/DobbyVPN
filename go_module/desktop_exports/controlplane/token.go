package controlplane

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ControlTokenPath() (string, error) {
	if path := os.Getenv("DOBBYVPN_CONTROL_TOKEN_PATH"); path != "" {
		return path, nil
	}
	return platformControlTokenPath()
}

func tokenPathInUserConfig() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "DobbyVPN", "control.token"), nil
}

func LoadOrCreateControlToken() (string, error) {
	path, err := ControlTokenPath()
	if err != nil {
		return "", err
	}
	if value, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(value))
		if len(token) != 64 {
			return "", errors.New("invalid desktop control token")
		}
		if _, err := hex.DecodeString(token); err != nil {
			return "", errors.New("invalid desktop control token")
		}
		return token, verifyControlTokenPermissions(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return LoadOrCreateControlToken()
	}
	if err != nil {
		return "", err
	}
	if _, err = fmt.Fprintln(f, token); err != nil {
		_ = f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	if err = secureControlTokenFile(path); err != nil {
		return "", err
	}
	return token, verifyControlTokenPermissions(path)
}
