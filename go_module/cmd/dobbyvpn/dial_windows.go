//go:build windows

package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go_module/desktop_exports/controlplane"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type controlTokenCredentials struct{ token string }

func (c controlTokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{controlplane.TokenMetadata: c.token}, nil
}

func (controlTokenCredentials) RequireTransportSecurity() bool { return false }

func windowsControlToken() (string, error) {
	path := os.Getenv("DOBBYVPN_CONTROL_TOKEN_PATH")
	if path == "" {
		programData := os.Getenv("PROGRAMDATA")
		if programData == "" {
			return "", fmt.Errorf("PROGRAMDATA is required for the installation control token")
		}
		path = filepath.Join(programData, "DobbyVPN", "control.token")
	}
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(value))
	if len(token) != 64 {
		return "", fmt.Errorf("invalid desktop control token")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", fmt.Errorf("invalid desktop control token")
	}
	return token, nil
}

func dialService() (*grpc.ClientConn, error) {
	address := os.Getenv("DOBBYVPN_CONTROL_ADDRESS")
	if address == "" {
		address = "127.0.0.1:50051"
	}
	token, err := windowsControlToken()
	if err != nil {
		return nil, err
	}
	return grpc.Dial(address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(controlTokenCredentials{token: token}),
	)
}
