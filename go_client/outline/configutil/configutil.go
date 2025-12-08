package configutil

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)


func NormalizeTransportConfig(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("config is required")
	}
	if strings.Contains(raw, "|") {
		return raw, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("failed to parse config: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "ss") {
		return raw, nil
	}

	query := u.Query()
	if query.Get("outline") != "1" {
		return raw, nil
	}


	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	pathWithQuery := path
	if rawQuery := u.RawQuery; rawQuery != "" {
		pathWithQuery = path + "?" + rawQuery
	}

	query.Del("outline")
	u.Path = ""
	u.RawQuery = query.Encode()

	var parts []string

	if host := u.Hostname(); host != "" {
		tlsValues := url.Values{}
		tlsValues.Set("sni", host)
		tlsValues.Set("certname", host)
		parts = append(parts, "tls:"+tlsValues.Encode())
	}

	wsValues := url.Values{}
	wsValues.Set("tcp_path", pathWithQuery)
	wsValues.Set("udp_path", pathWithQuery)
	parts = append(parts, "ws:"+wsValues.Encode())

	parts = append(parts, u.String())
	return strings.Join(parts, "|"), nil
}


func ExtractShadowsocksHost(config string) (string, error) {
	config = strings.TrimSpace(config)
	if config == "" {
		return "", errors.New("config is required")
	}

	parts := strings.Split(config, "|")
	for i := len(parts) - 1; i >= 0; i-- {
		part := strings.TrimSpace(parts[i])
		if part == "" {
			continue
		}
		u, err := url.Parse(part)
		if err != nil {
			return "", fmt.Errorf("failed to parse config part: %w", err)
		}
		if strings.EqualFold(u.Scheme, "ss") {
			host := u.Hostname()
			if host == "" {
				return "", errors.New("shadowsocks host not specified")
			}
			return host, nil
		}
	}
	return "", errors.New("shadowsocks config not found")
}
