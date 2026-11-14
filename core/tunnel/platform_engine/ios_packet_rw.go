package platform_engine

import "io"

// headerStrippedRW adapts a raw utun descriptor, whose every read carries a
// leading address-family header, for iobased.Endpoint. The endpoint compares
// the length returned by Read with the MTU and slices data[offset:offset+n]
// as the packet, so it expects n to be the IP packet length. A raw read
// returns header+packet, which made the endpoint drop IP packets of
// mtu-header+1 .. mtu bytes and hand the stack header-many trailing zero
// bytes. Read keeps the raw layout in p (header first, packet at p[offset:])
// but reports only the packet length. Packets longer than the MTU are
// reported as mtu+1 so the endpoint drops them instead of seeing a truncated
// read. Writes pass through unchanged.
//
// Read is not safe for concurrent use: iobased runs a single reader.
type headerStrippedRW struct {
	rw      io.ReadWriter
	offset  int
	mtu     int
	scratch []byte
}

func newHeaderStrippedRW(rw io.ReadWriter, mtu uint32, offset int) *headerStrippedRW {
	return &headerStrippedRW{
		rw:     rw,
		offset: offset,
		mtu:    int(mtu),
		// One byte beyond header+MTU tells an oversize packet from a full one.
		scratch: make([]byte, offset+int(mtu)+1),
	}
}

func (r *headerStrippedRW) Read(p []byte) (int, error) {
	n, err := r.rw.Read(r.scratch)
	if err != nil {
		return 0, err
	}
	if n <= r.offset {
		return 0, nil // header only or empty: nothing for the endpoint to inject
	}
	packetLen := n - r.offset
	if packetLen > r.mtu {
		return r.mtu + 1, nil // endpoint drops n > mtu
	}
	if len(p) < n {
		return 0, io.ErrShortBuffer
	}
	copy(p, r.scratch[:n])
	return packetLen, nil
}

func (r *headerStrippedRW) Write(p []byte) (int, error) { return r.rw.Write(p) }
