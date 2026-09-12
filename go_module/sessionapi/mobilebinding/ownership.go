package mobilebinding

// tunnelFDs separates descriptor ownership from platform mechanics so the
// no-reuse invariant is testable without a mobile/native toolchain.
type tunnelFDs struct {
	open map[int32]fdOwner
}

type fdOwner struct {
	session    string
	generation uint64
}

func newTunnelFDs() tunnelFDs {
	return tunnelFDs{open: make(map[int32]fdOwner)}
}
func (o *tunnelFDs) reserve(fd int32, owner fdOwner) bool {
	if _, exists := o.open[fd]; exists {
		return false
	}
	o.open[fd] = owner
	return true
}
func (o *tunnelFDs) release(fd int32, owner fdOwner) {
	if current, ok := o.open[fd]; ok && current == owner {
		delete(o.open, fd)
	}
}
