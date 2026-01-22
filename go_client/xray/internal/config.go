package internal

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// GenerateXrayConfig creates a config that listens on local SOCKS and proxies via VLESS
func GenerateXrayConfig(socksPort int, vlessConfigStr string) (*core.Config, error) {

	// 1. Unmarshal the user's VLESS/Reality config into a generic map
	var userConfig map[string]interface{}
	if err := json.Unmarshal([]byte(vlessConfigStr), &userConfig); err != nil {
		return nil, fmt.Errorf("invalid user config: %w", err)
	}

	// 2. Override Inbounds to be a local SOCKS5 listener
	// We replace whatever inbound was in the config with our SOCKS inbound
	socksInbound := map[string]interface{}{
		"tag":      "socks-in",
		"port":     socksPort,
		"listen":   "127.0.0.1",
		"protocol": "socks",
		"settings": map[string]interface{}{
			"udp": true,
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls"},
		},
	}
	userConfig["inbounds"] = []interface{}{socksInbound}

	// 3. Convert the map back to JSON bytes
	finalJson, err := json.Marshal(userConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	// 4. Use serial.DecodeJSONConfig to parse JSON -> Xray Internal Config
	// This replaces core.LoadConfig and avoids the input type error
	jsonReader := bytes.NewReader(finalJson)
	infraConfig, err := serial.DecodeJSONConfig(jsonReader)
	if err != nil {
		return nil, fmt.Errorf("failed to decode xray json config: %w", err)
	}

	// 5. Build the Protobuf Config required by core.New()
	return infraConfig.Build()
}
