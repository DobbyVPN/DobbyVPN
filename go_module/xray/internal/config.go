package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// GenerateXrayConfig overwrites user's inbounds with a local SOCKS5 inbound
// (TCP+UDP) for use by tun2socks.
func GenerateXrayConfig(vlessConfigStr string, socksListen string, socksPort int, routingTableID int, uplinkIface string) (*core.Config, error) {

	var userConfig map[string]interface{}
	if err := json.Unmarshal([]byte(vlessConfigStr), &userConfig); err != nil {
		return nil, fmt.Errorf("invalid user config: %w", err)
	}

	if socksListen == "" {
		socksListen = "127.0.0.1"
	}
	if socksPort <= 0 || socksPort > 65535 {
		return nil, fmt.Errorf("invalid socksPort=%d", socksPort)
	}

	delete(userConfig, "routing")

	socksInbound := map[string]interface{}{
		"tag":      "socks-in",
		"protocol": "socks",
		"listen":   socksListen,
		"port":     socksPort,
		"settings": map[string]interface{}{
			"auth": "noauth",
			"udp":  true,
			"ip":   socksListen,
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", "tls", "quic"},
		},
	}

	userConfig["inbounds"] = []interface{}{socksInbound}

	if outbounds, ok := userConfig["outbounds"].([]interface{}); ok && len(outbounds) > 0 {
		if firstOut, ok := outbounds[0].(map[string]interface{}); ok {

			// Ensure streamSettings exists
			if _, hasStream := firstOut["streamSettings"]; !hasStream {
				firstOut["streamSettings"] = map[string]interface{}{}
			}
			streamSettings := firstOut["streamSettings"].(map[string]interface{})

			// Ensure sockopt exists
			if _, hasSockopt := streamSettings["sockopt"]; !hasSockopt {
				streamSettings["sockopt"] = map[string]interface{}{}
			}
			sockopt := streamSettings["sockopt"].(map[string]interface{})

			// Apply OS-specific Xray socket protections
			switch runtime.GOOS {
			case "linux", "android":
				// Linux/Android use SO_MARK (matches routingTableID)
				sockopt["mark"] = routingTableID
			case "windows", "darwin":
				// Windows/macOS bind directly to the physical uplink interface
				if uplinkIface != "" {
					sockopt["interface"] = uplinkIface
				}
			}
		}
	}

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
