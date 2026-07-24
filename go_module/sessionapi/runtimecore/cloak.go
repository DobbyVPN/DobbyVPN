package runtimecore

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// cloakTOML is the compatibility subset embedded in an Outline profile.
// It deliberately excludes Outline password and transport-url fields.
type cloakTOML struct {
	Cloak            bool   `toml:"Cloak"`
	Server           string `toml:"Server"`
	Port             any    `toml:"Port"`
	Transport        string `toml:"Transport"`
	ProxyMethod      string `toml:"ProxyMethod"`
	EncryptionMethod string `toml:"EncryptionMethod"`
	UID              string `toml:"UID"`
	PublicKey        string `toml:"PublicKey"`
	ServerName       string `toml:"ServerName"`
	RemoteHost       string `toml:"RemoteHost"`
	RemotePort       any    `toml:"RemotePort"`
	CDNOriginHost    string `toml:"CDNOriginHost"`
	CDNWsURLPath     string `toml:"CDNWsUrlPath"`
	NumConn          *int   `toml:"NumConn"`
	StreamTimeout    *int   `toml:"StreamTimeout"`
	BrowserSig       string `toml:"BrowserSig"`
}

// cloakJSON exactly matches the legacy CloakClientConfig schema. The local
// listener is supplied separately to StartCloakClient, never in JSON.
type cloakJSON struct {
	Transport        string `json:"Transport"`
	ProxyMethod      string `json:"ProxyMethod"`
	EncryptionMethod string `json:"EncryptionMethod"`
	UID              string `json:"UID"`
	PublicKey        string `json:"PublicKey"`
	ServerName       string `json:"ServerName"`
	NumConn          int    `json:"NumConn"`
	BrowserSig       string `json:"BrowserSig,omitempty"`
	StreamTimeout    *int   `json:"StreamTimeout,omitempty"`
	RemoteHost       string `json:"RemoteHost"`
	RemotePort       string `json:"RemotePort"`
	CDNWsURLPath     string `json:"CDNWsUrlPath,omitempty"`
	CDNOriginHost    string `json:"CDNOriginHost,omitempty"`
}

func outlineUsesCloak(raw []byte) bool {
	var cfg cloakTOML
	_, err := toml.Decode(string(raw), &cfg)
	return err == nil && cfg.Cloak
}

// NormalizeCloakProfile converts the legacy fields embedded in an Outline
// profile into the existing native Cloak JSON contract. It stays in this pure
// package so compatibility behavior is testable without linking native VPN
// libraries.
func NormalizeCloakProfile(raw []byte) ([]byte, error) {
	var source cloakTOML
	if _, err := toml.Decode(string(raw), &source); err != nil {
		return nil, fmt.Errorf("parse Outline Cloak TOML: %w", err)
	}
	if !source.Cloak {
		return nil, errors.New("Outline profile does not enable Cloak")
	}
	remoteHost := defaultString(source.RemoteHost, source.Server)
	remotePort := defaultString(portString(source.RemotePort), defaultString(portString(source.Port), "443"))
	serverName := defaultString(source.ServerName, source.Server)
	transport := strings.ToLower(strings.TrimSpace(source.Transport))
	if transport == "" {
		if strings.TrimSpace(source.CDNWsURLPath) != "" {
			transport = "cdn"
		} else {
			transport = "direct"
		}
	}
	if transport != "cdn" && transport != "direct" {
		return nil, errors.New("Cloak transport must be cdn or direct")
	}
	for _, required := range []struct{ field, value string }{
		{"EncryptionMethod", source.EncryptionMethod}, {"UID", source.UID}, {"PublicKey", source.PublicKey},
		{"ServerName", serverName}, {"RemoteHost", remoteHost}, {"RemotePort", remotePort},
	} {
		if strings.TrimSpace(required.value) == "" {
			return nil, fmt.Errorf("Cloak %s is required", required.field)
		}
	}
	streamTimeout := source.StreamTimeout
	if streamTimeout == nil {
		defaultTimeout := 300
		streamTimeout = &defaultTimeout
	}
	numConn := 8
	if source.NumConn != nil {
		numConn = *source.NumConn
	}
	config := cloakJSON{
		Transport:        "direct",
		ProxyMethod:      defaultString(source.ProxyMethod, "shadowsocks"),
		EncryptionMethod: source.EncryptionMethod,
		UID:              source.UID,
		PublicKey:        source.PublicKey,
		ServerName:       serverName,
		NumConn:          numConn,
		BrowserSig:       source.BrowserSig,
		StreamTimeout:    streamTimeout,
		RemoteHost:       remoteHost,
		RemotePort:       remotePort,
	}
	if transport == "cdn" {
		config.Transport = "CDN"
		config.CDNOriginHost = defaultString(source.CDNOriginHost, source.Server)
		config.CDNWsURLPath = source.CDNWsURLPath
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode Cloak configuration: %w", err)
	}
	return encoded, nil
}

func defaultString(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func portString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}
