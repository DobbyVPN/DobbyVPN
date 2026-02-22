package georouting

import "net/netip"

// IPTrie — компактный префиксный trie для быстрого матчинга IP по спискам CIDR.
type IPTrie struct {
	v4 *node4
	v6 *node6
}

type node4 struct {
	zero *node4
	one  *node4
	term bool
}

type node6 struct {
	zero *node6
	one  *node6
	term bool
}

func NewIPTrie() *IPTrie { return &IPTrie{} }

func (t *IPTrie) Add(prefix netip.Prefix) {
	prefix = prefix.Masked()
	addr := prefix.Addr().Unmap()
	bits := prefix.Bits()
	if bits < 0 {
		return
	}
	if addr.Is4() {
		if t.v4 == nil {
			t.v4 = &node4{}
		}
		insertV4(t.v4, addr.As4(), bits)
		return
	}
	if addr.Is6() {
		if t.v6 == nil {
			t.v6 = &node6{}
		}
		insertV6(t.v6, addr.As16(), bits)
		return
	}
}

func (t *IPTrie) Contains(addr netip.Addr) bool {
	addr = addr.Unmap()
	if addr.Is4() {
		if t.v4 == nil {
			return false
		}
		return containsV4(t.v4, addr.As4())
	}
	if addr.Is6() {
		if t.v6 == nil {
			return false
		}
		return containsV6(t.v6, addr.As16())
	}
	return false
}

func insertV4(root *node4, a [4]byte, bits int) {
	n := root
	if bits == 0 {
		n.term = true
		return
	}
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	for i := 0; i < bits; i++ {
		bit := (v >> (31 - i)) & 1
		if bit == 0 {
			if n.zero == nil {
				n.zero = &node4{}
			}
			n = n.zero
		} else {
			if n.one == nil {
				n.one = &node4{}
			}
			n = n.one
		}
	}
	n.term = true
}

func containsV4(root *node4, a [4]byte) bool {
	n := root
	if n.term {
		return true
	}
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	for i := 0; i < 32; i++ {
		bit := (v >> (31 - i)) & 1
		if bit == 0 {
			n = n.zero
		} else {
			n = n.one
		}
		if n == nil {
			return false
		}
		if n.term {
			return true
		}
	}
	return n != nil && n.term
}

func insertV6(root *node6, a [16]byte, bits int) {
	n := root
	if bits == 0 {
		n.term = true
		return
	}
	for i := 0; i < bits; i++ {
		byteIdx := i / 8
		bitIdx := 7 - (i % 8)
		bit := (a[byteIdx] >> bitIdx) & 1
		if bit == 0 {
			if n.zero == nil {
				n.zero = &node6{}
			}
			n = n.zero
		} else {
			if n.one == nil {
				n.one = &node6{}
			}
			n = n.one
		}
	}
	n.term = true
}

func containsV6(root *node6, a [16]byte) bool {
	n := root
	if n.term {
		return true
	}
	for i := 0; i < 128; i++ {
		byteIdx := i / 8
		bitIdx := 7 - (i % 8)
		bit := (a[byteIdx] >> bitIdx) & 1
		if bit == 0 {
			n = n.zero
		} else {
			n = n.one
		}
		if n == nil {
			return false
		}
		if n.term {
			return true
		}
	}
	return n != nil && n.term
}
