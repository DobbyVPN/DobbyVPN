package tunnel

import (
	"fmt" // Обязательно добавь
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	log "go_client/logger"
)

var (
	transferMu sync.Mutex
	isRunning  bool
)

func StartEngine(fd int, proxyAddr string) {
	transferMu.Lock()
	defer transferMu.Unlock()

	if isRunning {
		stopLocked()
	}

	// ИСПРАВЛЕНИЕ: Используем fmt.Sprintf для корректного формирования пути к FD
	devicePath := fmt.Sprintf("fd://%d", fd)
	proxyURL := fmt.Sprintf("socks5://%s", proxyAddr)

	log.Infof("[Engine] Intializing with Device: %s, Proxy: %s", devicePath, proxyURL)

	key := &engine.Key{
		Proxy:    proxyURL,
		Device:   devicePath,
		LogLevel: "info",
		MTU:      1500,
	}

	// Загружаем конфиг и запускаем
	engine.Insert(key)

	// Внимание: если engine.Start() вызывает панику,
	// наш defer recover в client_mobile его поймает.
	engine.Start()

	isRunning = true
}

func StopEngine() {
	transferMu.Lock()
	defer transferMu.Unlock()
	if isRunning {
		stopLocked()
	}
}

func stopLocked() {
	engine.Stop()
	isRunning = false
}
