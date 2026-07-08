package internal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"go_module/dnscache"
	"go_module/log"
	"go_module/tunnel/protected_dialer"
	xrayCommon "go_module/xray/common"

	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
)

// GenerateXrayConfig overwrites user's inbounds with a local SOCKS5 inbound
// (TCP+UDP) for use by tun2socks.
func GenerateXrayConfig(vlessConfigStr string, socksListen string, socksPort int, routingTableID int, uplinkIface string, user string, pass string) (*core.Config, error) {

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
			"destOverride": []string{"http", "tls", "quic"},
		},
	}

	userConfig["inbounds"] = []interface{}{socksInbound}

	ensureXrayLogConfig(userConfig)
	applyResolvedOutboundAddresses(userConfig)
	applyProtectedSockopt(userConfig, routingTableID, uplinkIface)

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

func ensureXrayLogConfig(userConfig map[string]interface{}) {
	logConfig, ok := userConfig["log"].(map[string]interface{})
	if !ok {
		logConfig = map[string]interface{}{}
		userConfig["log"] = logConfig
	}

	level, _ := logConfig["loglevel"].(string)
	normalizedLevel := strings.ToLower(level)
	switch normalizedLevel {
	case "debug", "info", "warning", "error":
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

func applyResolvedOutboundAddresses(userConfig map[string]interface{}) {
	outbounds, ok := userConfig["outbounds"].([]interface{})
	if !ok {
		log.Debugf(xrayCommon.Category, "protected DNS rewrite skipped: outbounds missing or invalid type")
		return
	}

	updated := 0
	skipped := 0
	for _, outbound := range outbounds {
		outboundMap, ok := outbound.(map[string]interface{})
		if !ok {
			skipped++
			continue
		}
		settings, ok := outboundMap["settings"].(map[string]interface{})
		if !ok {
			skipped++
			continue
		}
		vnext, ok := settings["vnext"].([]interface{})
		if !ok {
			skipped++
			continue
		}

		for _, server := range vnext {
			serverMap, ok := server.(map[string]interface{})
			if !ok {
				skipped++
				continue
			}
			address, ok := serverMap["address"].(string)
			if !ok || address == "" {
				skipped++
				continue
			}
			if ip := net.ParseIP(address); ip != nil {
				if ip.To4() != nil {
					skipped++
					continue
				}
				log.Debugf(xrayCommon.Category, "protected DNS rewrite skipped: IPv6 address=%q", address)
				skipped++
				continue
			}

			ip4, err := dnscache.ResolveIPv4(context.Background(), address, dnscache.FastResolveTimeout, "xray-config")
			if err != nil {
				log.Debugf(xrayCommon.Category, "protected DNS rewrite skipped: address=%s timeout=%s err=%v", address, dnscache.FastResolveTimeout, err)
				skipped++
				continue
			}
			appliedNames := applyOriginalServerName(outboundMap, address)
			serverMap["address"] = ip4.String()
			updated++
			log.Debugf(xrayCommon.Category, "protected DNS rewrite applied: address=%s resolved=%s preservedNames=%v", address, ip4.String(), appliedNames)
		}
	}

	log.Debugf(xrayCommon.Category, "protected DNS rewrite complete: outbounds=%d updated=%d skipped=%d", len(outbounds), updated, skipped)
}

func applyOriginalServerName(outboundMap map[string]interface{}, originalAddress string) []string {
	streamSettings, ok := outboundMap["streamSettings"].(map[string]interface{})
	if !ok || originalAddress == "" {
		return nil
	}

	applied := make([]string, 0, 4)
	if streamSecurity(streamSettings) == "tls" {
		tlsSettings := ensureMap(streamSettings, "tlsSettings")
		if setStringIfMissing(tlsSettings, "serverName", originalAddress) {
			applied = append(applied, "tlsSettings.serverName")
		}
	}
	if streamSecurity(streamSettings) == "reality" {
		realitySettings := ensureMap(streamSettings, "realitySettings")
		if setStringIfMissing(realitySettings, "serverName", originalAddress) {
			applied = append(applied, "realitySettings.serverName")
		}
	}
	for _, key := range []string{"xhttpSettings", "splithttpSettings"} {
		xhttpSettings, ok := streamSettings[key].(map[string]interface{})
		if !ok {
			continue
		}
		if setStringIfMissing(xhttpSettings, "host", originalAddress) {
			applied = append(applied, key+".host")
		}
		if downloadSettings, ok := xhttpSettings["downloadSettings"].(map[string]interface{}); ok {
			applied = append(applied, applyOriginalServerNameToStream(downloadSettings, originalAddress, key+".downloadSettings")...)
		}
	}
	if grpcSettings, ok := streamSettings["grpcSettings"].(map[string]interface{}); ok {
		if setStringIfMissing(grpcSettings, "authority", originalAddress) {
			applied = append(applied, "grpcSettings.authority")
		}
	}
	return applied
}

func applyOriginalServerNameToStream(streamSettings map[string]interface{}, originalAddress string, prefix string) []string {
	applied := make([]string, 0, 2)
	switch streamSecurity(streamSettings) {
	case "tls":
		tlsSettings := ensureMap(streamSettings, "tlsSettings")
		if setStringIfMissing(tlsSettings, "serverName", originalAddress) {
			applied = append(applied, prefix+".tlsSettings.serverName")
		}
	case "reality":
		realitySettings := ensureMap(streamSettings, "realitySettings")
		if setStringIfMissing(realitySettings, "serverName", originalAddress) {
			applied = append(applied, prefix+".realitySettings.serverName")
		}
	}
	return applied
}

func streamSecurity(streamSettings map[string]interface{}) string {
	security, _ := streamSettings["security"].(string)
	return strings.ToLower(security)
}

func ensureMap(parent map[string]interface{}, key string) map[string]interface{} {
	value, ok := parent[key].(map[string]interface{})
	if ok {
		return value
	}
	value = map[string]interface{}{}
	parent[key] = value
	return value
}

func setStringIfMissing(settings map[string]interface{}, key string, value string) bool {
	existing, _ := settings[key].(string)
	if existing != "" {
		return false
	}
	settings[key] = value
	return true
}

func applyProtectedSockopt(userConfig map[string]interface{}, routingTableID int, uplinkIface string) {
	protectedSockopt := protected_dialer.XraySockopt(routingTableID, uplinkIface)
	if len(protectedSockopt) == 0 {
		log.Debugf(xrayCommon.Category, "protected sockopt skipped: routingTableID=%d uplinkIface=%q", routingTableID, uplinkIface)
		return
	}

	outbounds, ok := userConfig["outbounds"].([]interface{})
	if !ok {
		log.Debugf(xrayCommon.Category, "protected sockopt skipped: outbounds missing or invalid type")
		return
	}

	updated := 0
	skipped := 0
	for _, outbound := range outbounds {
		outboundMap, ok := outbound.(map[string]interface{})
		if !ok {
			skipped++
			continue
		}

		streamSettings := ensureMap(outboundMap, "streamSettings")
		applySockoptToStreamSettings(streamSettings, protectedSockopt)
		updated++
	}

	log.Debugf(
		xrayCommon.Category,
		"protected sockopt applied: policy=%v outbounds=%d updated=%d skipped=%d routingTableID=%d uplinkIface=%q",
		protectedSockopt,
		len(outbounds),
		updated,
		skipped,
		routingTableID,
		uplinkIface,
	)
}

func applySockoptToStreamSettings(streamSettings map[string]interface{}, protectedSockopt map[string]interface{}) {
	sockopt := ensureMap(streamSettings, "sockopt")
	for key, value := range protectedSockopt {
		sockopt[key] = value
	}

	for _, key := range []string{"xhttpSettings", "splithttpSettings"} {
		xhttpSettings, ok := streamSettings[key].(map[string]interface{})
		if !ok {
			continue
		}
		if downloadSettings, ok := xhttpSettings["downloadSettings"].(map[string]interface{}); ok {
			applySockoptToStreamSettings(downloadSettings, protectedSockopt)
		}
	}
}
