package common

import (
	"errors"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
)

// resetTexts are the platform error wordings a reset or aborted write can
// carry when it is not an errors.Is match. They stay in one place so every
// stream diagnostic classifies the same way.
var resetTexts = []string{
	"connection reset by peer",
	"forcibly closed by the remote host",
	"aborted by the software in your host machine",
}

// StreamErrorClass classifies a terminal stream error for diagnostic records.
// The result is one of "fin", "rst", "timeout", "closed" or "error"; callers
// add "dial" and "handshake" at their dial sites. It never echoes the error's
// addresses or text.
func StreamErrorClass(err error) string {
	if err == nil {
		return "closed"
	}
	if errors.Is(err, io.EOF) {
		return "fin"
	}
	if errors.Is(err, net.ErrClosed) {
		return "closed"
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return "rst"
	}
	text := strings.ToLower(err.Error())
	for _, wording := range resetTexts {
		if strings.Contains(text, wording) {
			return "rst"
		}
	}
	// net.Error also demands Temporary(); os.IsTimeout-style checks only need
	// Timeout, which is what wrapped dial and socket deadlines provide.
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return "error"
}

// NoticeBudget bounds how many noteworthy diagnostic records one engine
// generation or device emits before they drop to debug, so a failure storm
// cannot flood the retained log.
type NoticeBudget struct {
	used  atomic.Int64
	limit int64
}

// NewNoticeBudget returns a budget that allows limit noteworthy records.
func NewNoticeBudget(limit int64) *NoticeBudget {
	return &NoticeBudget{limit: limit}
}

// Take reports whether the caller may log at its noteworthy level and whether
// this call is the one that exhausted the budget.
func (b *NoticeBudget) Take() (ok bool, exhaustedNow bool) {
	if b == nil {
		return true, false
	}
	n := b.used.Add(1)
	return n <= b.limit, n == b.limit+1
}
