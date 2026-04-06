package config

type Peer struct {
	PublicKey           Key
	PresharedKey        Key
	AllowedIPs          []IPCidr
	Endpoint            Endpoint
	PersistentKeepalive uint16

	RxBytes           Bytes
	TxBytes           Bytes
	LastHandshakeTime HandshakeTime
}
