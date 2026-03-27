//go:build !ios

package internal

func AdaptReadPackets(raw []byte) ([]byte, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

func AdaptWritePackets(packet []byte) ([]byte, bool) {
	if len(packet) == 0 {
		return nil, false
	}
	return packet, true
}
