package cloak_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"testing"

	"go_client/cloak"
	"go_client/common"

	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
)

func TestShadowsocksTransportConfigFunctional(t *testing.T) {
	providers := configurl.NewDefaultProviders()

	hostPort := net.JoinHostPort("127.0.0.1", "8388")
	method := "aes-256-gcm"
	password := "functional-password"

	plain := fmt.Sprintf("ss://%s:%s@%s", method, password, hostPort)
	creds := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	sip002 := fmt.Sprintf("ss://%s@%s", creds, hostPort)

	tests := []struct {
		name        string
		cfg         string
		wantErr     bool
		errContains string
	}{
		{name: "plain config", cfg: plain, wantErr: false},
		{name: "sip002 config", cfg: sip002, wantErr: false},
		{
			name:        "wss chain malformed ws option",
			cfg:         "tls:sni=localhost|ws:path=/ws|" + plain,
			wantErr:     true,
			errContains: "unsupported option",
		},
		{
			name:        "missing ss part in chain",
			cfg:         "tls:sni=localhost|ws:path=/ws",
			wantErr:     true,
			errContains: "unsupported option",
		},
		{
			name:        "malformed ss credentials",
			cfg:         "ss://broken-credentials@127.0.0.1:8388",
			wantErr:     true,
			errContains: "illegal base64",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := providers.NewStreamDialer(context.Background(), tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for config %q, got nil", tc.cfg)
				}
				if tc.errContains != "" && !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.errContains)) {
					t.Fatalf("expected error to contain %q, got %q", tc.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for config %q: %v", tc.cfg, err)
			}
		})
	}
}

func TestCloakTransportConfigValidationFunctional(t *testing.T) {
	originalClient := common.Client
	isolatedClient := &common.CommonClient{}
	common.Client = isolatedClient
	t.Cleanup(func() { common.Client = originalClient })

	tests := []struct {
		name   string
		config string
	}{
		{
			name:   "broken json",
			config: `{"UID":"abc"`,
		},
		{
			name:   "invalid UID/PublicKey/RemoteHost",
			config: `{"UID":"%%%bad%%%", "PublicKey":"@@@bad@@@", "RemoteHost":"invalid.invalid.host"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cloak.StartCloakClient("127.0.0.1", "0", tc.config, false)
			t.Cleanup(cloak.StopCloakClient)

			if active := isolatedClient.GetClientNames(true); len(active) != 0 {
				t.Fatalf("expected no active cloak client for invalid config, got %v", active)
			}
		})
	}
}
