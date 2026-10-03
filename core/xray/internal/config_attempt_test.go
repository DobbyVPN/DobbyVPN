package internal

import (
	"reflect"
	"testing"
	"time"

	"core/dnscache"
	"core/protocol"

	xray "github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func TestAttemptConfigurationDoesNotMutateAcceptedFields(t *testing.T) {
	accepted := map[string]any{
		"log":     map[string]any{"loglevel": "warning"},
		"routing": map[string]any{"domainStrategy": "AsIs"},
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": "vpn.example.invalid", "port": 443,
				"users": []any{map[string]any{"id": "11111111-1111-1111-1111-111111111111", "encryption": "none"}},
			}}},
			"streamSettings": map[string]any{"security": "tls"},
		}},
	}
	before := protocol.CloneFields(accepted)
	var previous *xray.Config
	for _, address := range []string{"192.0.2.1", "192.0.2.2"} {
		cache := dnscache.New()
		if !cache.SetIPv4("vpn.example.invalid", address, "test", time.Minute) {
			t.Fatal("cache rejected address")
		}
		configured, err := GenerateXrayConfig(accepted, "127.0.0.1", 1080, 0, "", "user", "password", cache)
		if err != nil {
			t.Fatal(err)
		}
		if previous != nil && proto.Equal(previous, configured) {
			t.Fatal("new attempt reused the previous DNS address")
		}
		if !reflect.DeepEqual(before, accepted) {
			t.Fatalf("attempt modified accepted configuration: %#v", accepted)
		}
		previous = configured
	}
}
