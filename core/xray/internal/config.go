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
	// Xray filters messages before invoking our handler. Capture info when the
	// requested threshold is warning so the bridge can promote tunnel resets;
	// the bridge still enforces the requested threshold after classification.
	captureXrayLogMessages(userConfig)
	applyGrpcKeepaliveDefaults(userConfig)

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

func captureXrayLogMessages(userConfig map[string]interface{}) {
	logConfig := userConfig["log"].(map[string]interface{})
	if logConfig["loglevel"] == xrayLogLevelWarning {
		logConfig["loglevel"] = xrayLogLevelInfo
	}
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
	case xrayLogLevelDebug, xrayLogLevelInfo, xrayLogLevelWarning, xrayLogLevelError:
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

// applyGrpcKeepaliveDefaults makes gRPC outbounds send HTTP/2 keepalive PINGs
// even when a stream is idle, so a 60s idle timeout in front of the server
// doesn't reset the connection (see issue #342). It never overrides a value
// the user's own config already set.
func applyGrpcKeepaliveDefaults(userConfig map[string]interface{}) {
	outbounds, ok := userConfig["outbounds"].([]interface{})
	if !ok {
		return
	}

	updated := 0
	for _, outbound := range outbounds {
		outboundMap, ok := outbound.(map[string]interface{})
		if !ok {
			continue
		}
		streamSettings, ok := outboundMap["streamSettings"].(map[string]interface{})
		if !ok {
			continue
		}
		if streamSettings["network"] != "grpc" {
			continue
		}
		grpcSettings, ok := streamSettings["grpcSettings"].(map[string]interface{})
		if !ok {
			if _, present := streamSettings["grpcSettings"]; present && streamSettings["grpcSettings"] != nil {
				continue
			}
			grpcSettings = make(map[string]interface{})
			streamSettings["grpcSettings"] = grpcSettings
		}

		changed := false
		if setIntIfMissing(grpcSettings, "idle_timeout", defaultGrpcIdleTimeoutSeconds) {
			changed = true
		}
		if setIntIfMissing(grpcSettings, "health_check_timeout", defaultGrpcHealthCheckTimeoutSeconds) {
			changed = true
		}
		if setBoolIfMissing(grpcSettings, "permit_without_stream", defaultGrpcPermitWithoutStream) {
			changed = true
		}
		if changed {
			updated++
		}
	}

	log.Debugf(xrayCommon.Category, "grpc keepalive defaults applied: outbounds=%d updated=%d", len(outbounds), updated)
}

func setIntIfMissing(settings map[string]interface{}, key string, value int) bool {
	if _, ok := settings[key]; ok {
		return false
	}
	settings[key] = value
	return true
}

func setBoolIfMissing(settings map[string]interface{}, key string, value bool) bool {
	if _, ok := settings[key]; ok {
		return false
	}
	settings[key] = value
	return true
}
