package cloak

import (
	"go_client/common"
	"testing"
)

func TestStartCloakClientInvalidConfigsFunctional(t *testing.T) {
	originalClient := common.Client
	isolatedClient := &common.CommonClient{}
	common.Client = isolatedClient
	t.Cleanup(func() { common.Client = originalClient })

	cases := []struct {
		name   string
		config string
	}{
		{
			name:   "broken json",
			config: `{"UID":"abc"`,
		},
		{
			name: "invalid uid/public key/remote host",
			config: `{
				"UID":"%%%bad-base64%%%",
				"PublicKey":"@@@bad-base64@@@",
				"ServerName":"example.com",
				"RemoteHost":"definitely.invalid.host.name",
				"RemotePort":"18445"
			}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			StartCloakClient("127.0.0.1", "0", tc.config, false)
			t.Cleanup(StopCloakClient)

			if active := isolatedClient.GetClientNames(true); len(active) != 0 {
				t.Fatalf("expected no active cloak client for invalid config, got %v", active)
			}
		})
	}
}

func TestStopCloakClientIdempotentFunctional(t *testing.T) {
	originalClient := common.Client
	isolatedClient := &common.CommonClient{}
	common.Client = isolatedClient
	t.Cleanup(func() { common.Client = originalClient })

	StopCloakClient()
	StopCloakClient()

	if active := isolatedClient.GetClientNames(true); len(active) != 0 {
		t.Fatalf("expected no active clients after idempotent stop, got %v", active)
	}
	if !isolatedClient.CouldStart() {
		t.Fatal("expected client to leave no critical sections after stop")
	}
}
