package tunnel

import (
	"time"

	"go_client/georouting"
)

type DomainSniffer interface {
	// Sniff может извлечь домен (SNI/HTTP Host) из первых пакетов потока.
	// Если не умеешь/не нужно — просто не передавай sniffer.
	Sniff(packet []byte, meta georouting.Metadata) (domain string, ok bool)
}

type Detour struct {
	Tag     georouting.Outbound
	ReadFn  ReaderFunc
	WriteFn WriterFunc
}

type FlowCacheOptions struct {
	Enabled        bool
	TCPIdleTimeout time.Duration
	UDPIdleTimeout time.Duration
	GCInterval     time.Duration
	MaxEntries     int
}

func defaultFlowCacheOptions() FlowCacheOptions {
	return FlowCacheOptions{
		Enabled:        true,
		TCPIdleTimeout: 2 * time.Minute,
		UDPIdleTimeout: 30 * time.Second,
		GCInterval:     15 * time.Second,
		MaxEntries:     200_000,
	}
}

type TransferOptions struct {
	Router        georouting.Router
	DomainSniffer DomainSniffer
	Detours       []Detour

	FlowCache FlowCacheOptions
}
