package main

import (
 	"github.com/pelletier/go-toml/v2"
	"encoding/base64"
	"fmt"
)

 type SSConfig struct {
 	Server   string `toml:"server"`
 	Port     int    `toml:"port"`
 	Method   string `toml:"method"`
 	Password string `toml:"password"`
 	Outline  bool   `toml:"outline"`
 }

 type ShadowsocksBlock struct {
 	Direct *SSConfig `toml:"direct"`
 	Local  *SSConfig `toml:"local"`
 }

 type RootConfig struct {
 	Shadowsocks ShadowsocksBlock `toml:"shadowsocks"`
 }

 func ParseSSTOML(tomlStr string) (string, error) {
 	var cfg RootConfig
 	if err := toml.Unmarshal([]byte(tomlStr), &cfg); err != nil {
 		return "", fmt.Errorf("failed to parse toml: %w", err)
 	}
 	var ss *SSConfig
 	if cfg.Shadowsocks.Direct != nil {
 		ss = cfg.Shadowsocks.Direct
 	} else if cfg.Shadowsocks.Local != nil {
 		ss = cfg.Shadowsocks.Local
 	} else {
 		return "", fmt.Errorf("no [shadowsocks.direct] or [shadowsocks.local] section found")
 	}

 	if ss.Server == "" || ss.Port == 0 || ss.Method == "" || ss.Password == "" {
 		return "", fmt.Errorf("incomplete shadowsocks config")
 	}


 	userInfo := ss.Method + ":" + ss.Password
 	b64 := base64.RawURLEncoding.EncodeToString([]byte(userInfo))

 	uri := fmt.Sprintf("ss://%s@%s:%d/", b64, ss.Server, ss.Port)
 	if ss.Outline {
 		uri += "?outline=1"
 	}
 	return uri, nil
 }

func main() {
	// Тестовый TOML
	tomlStr := `
version = "0.3"
protocol="shadowsocks"

[shadowsocks.direct]
server = "85.9.223.19"
port = 22484
method = "chacha20-ietf-poly1305"
password = "p3ItM8SfXhvSZQBdlu5w23"
outline = true
`

	uri, err := ParseSSTOML(tomlStr)
	if err != nil {
		fmt.Println("Ошибка:", err)
		return
	}
	fmt.Println("Сгенерированный URI:", uri)
}