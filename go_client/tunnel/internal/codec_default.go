//go:build !ios

package internal

func DecodePacket(raw []byte) ([]byte, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	return raw, true
}

func EncodePacket(packet []byte) ([]byte, bool) {
	if len(packet) == 0 {
		return nil, false
	}
	return packet, true
}
