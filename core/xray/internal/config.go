package internal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"core/dnscache"
	"core/log"
	"core/protocol"
	xrayCommon "core/xray/common"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all" // Register Xray's built-in protocol and transport modules.
)

// GenerateXrayConfig overwrites user's inbounds with a local SOCKS5 inbound
// (TCP+UDP) for use by tun2socks.
func GenerateXrayConfig(
	accepted map[string]any, socksListen string,
	socksPort, routingTableID int,
	uplinkIface, user, pass string,
	dnsCache *dnscache.Cache,
) (*core.Config, error) {
	if dnsCache == nil {
		return nil, errors.New("DNS cache is required")
	}

	userConfig := protocol.CloneFields(accepted)

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
			"auth": "password",
			"accounts": []map[string]string{
				{
					"user": user,
					"pass": pass,
				},
			},
			"udp": true,
			"ip":  socksListen,
		},
		"sniffing": map[string]interface{}{
			"enabled":      true,
			"destOverride": []string{"http", xrayStreamSecurityTLS, "quic"},
		},
	}

	userConfig["inbounds"] = []interface{}{socksInbound}

	ensureXrayLogConfig(userConfig)

	finalJSON, err := json.Marshal(userConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal config: %w", err)
	}

	jsonReader := bytes.NewReader(finalJSON)
	infraConfig, err := serial.DecodeJSONConfig(jsonReader)
	if err != nil {
		return nil, fmt.Errorf("failed to decode xray json config: %w", err)
	}

	return infraConfig.Build()
}

func ensureXrayLogConfig(userConfig map[string]interface{}) {
	logConfig, ok := userConfig["log"].(map[string]interface{})
	if !ok {
		logConfig = map[string]interface{}{}
		userConfig["log"] = logConfig
	}

	level, _ := logConfig["loglevel"].(string)
	normalizedLevel := strings.ToLower(level)
	switch normalizedLevel {
	case defaultXrayLogLevelName, xrayLogLevelInfo, xrayLogLevelWarning, xrayLogLevelError:
		if level != normalizedLevel {
			logConfig["loglevel"] = normalizedLevel
		}
		log.Debugf(xrayCommon.Category, "xray log config kept: level=%s", normalizedLevel)
	default:
		logConfig["loglevel"] = defaultXrayLogLevelName
		if level == "" {
			log.Debugf(xrayCommon.Category, "xray log config applied: level=%s reason=missing", defaultXrayLogLevelName)
		} else {
			log.Debugf(xrayCommon.Category, "xray log config applied: level=%s reason=unsupported previous=%q", defaultXrayLogLevelName, level)
		}
	}
}
