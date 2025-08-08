package cloak_outline

import (
	"go_client/outline"
)

var client *outline.OutlineClient

// NewOutlineClient — инициализация глобального клиента
func NewOutlineClient(config string) {
	cl := outline.NewClient(config)
	client = cl
}

// Connect — подключение VPN
func Connect() {
	if client != nil {
		client.Connect()
	}
}

// Disconnect — отключение VPN
func Disconnect() {
	if client != nil {
		client.Disconnect()
	}
}

// Read — чтение данных
func Read(maxLen int) []byte {
	if client == nil {
		return nil
	}
	data, err := client.Read()
	if err != nil {
		return nil
	}
	if len(data) > maxLen {
		return data[:maxLen]
	}
	return data
}

// Write — запись данных
func Write(data []byte) int {
	if client == nil {
		return -1
	}
	n, err := client.Write(data)
	if err != nil {
		return -1
	}
	return n
}
