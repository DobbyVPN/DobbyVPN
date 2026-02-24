package internal

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// GenerateXrayConfig creates a config with a TUN inbound
// tunName: Can be an interface name (Desktop) or "fd://123" (Mobile)
func GenerateXrayConfig(tunName string, vlessConfigStr string) (*core.Config, error) {

	// 1. Unmarshal user config
	var userConfig map[string]interface{}
	if err := json.Unmarshal([]byte(vlessConfigStr), &userConfig); err != nil {
		return nil, fmt.Errorf("invalid user config: %w", err)
	}

	// 2. Define the TUN Inbound
	tunInbound := map[string]interface{}{
		"tag":      "tun-in",
		"protocol": "tun",
		"settings": map[string]interface{}{
			"name": tunName, // "wintun", "tun0", or "fd://123"
			"mtu":  1500,
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
		},
	}

	// 3. Replace Inbounds
	userConfig["inbounds"] = []interface{}{tunInbound}

	// 4. Decode and Build
	finalJson, err := json.Marshal(userConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	jsonReader := bytes.NewReader(finalJson)
	infraConfig, err := serial.DecodeJSONConfig(jsonReader)
	if err != nil {
		return nil, fmt.Errorf("failed to decode xray json config: %w", err)
	}

	return infraConfig.Build()
}
