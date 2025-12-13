package configutil

import (
	"errors"
	"net/url"
	"strings"
)

// ExtractShadowsocksHost извлекает хост shadowsocks сервера из конфига.
// Поддерживает форматы:
//   - ss://...@host:port
//   - ws:...|ss://...@host:port
//   - tls:...|ws:...|ss://...@host:port
func ExtractShadowsocksHost(config string) (string, error) {
	config = strings.TrimSpace(config)
	if config == "" {
		return "", errors.New("config is required")
	}

	// Ищем ss:// часть в конфиге (может быть после | разделителей)
	parts := strings.Split(config, "|")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "ss://") {
			u, err := url.Parse(part)
			if err != nil {
				return "", err
			}
			host := u.Hostname()
			if host == "" {
				return "", errors.New("shadowsocks host not specified")
			}
			return host, nil
		}
	}

	return "", errors.New("shadowsocks config (ss://) not found")
}
