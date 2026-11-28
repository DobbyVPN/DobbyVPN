package outline

import (
	"net"
	"sync/atomic"

	"core/common"
	"core/log"
)

// proxyLegNoticeLimit bounds the proxy-leg records one device emits before
// they drop to debug.
const proxyLegNoticeLimit = 20

// proxyLegConn observes the proxy-side leg of one SOCKS stream so a server
// reset or timeout is recorded with its destination, direction and reason.
type proxyLegConn struct {
	net.Conn
	dst     string
	noticed *common.NoticeBudget
	first   atomic.Bool
}

func (c *proxyLegConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.noticeError("read", err)
	return n, err
}

func (c *proxyLegConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	c.noticeError("write", err)
	return n, err
}

func (c *proxyLegConn) noticeError(direction string, err error) {
	if err == nil {
		return
	}
	class := common.StreamErrorClass(err)
	if class == "fin" || class == "closed" {
		return
	}
	if !c.first.CompareAndSwap(false, true) {
		return
	}
	ok, exhaustedNow := c.noticed.Take()
	record := log.Warnf
	if !ok {
		record = log.Debugf
	}
	record(Category, "[SOCKS5 TCP] proxy leg end dst=%s class=%s direction=%s err=%v", c.dst, class, direction, err)
	if exhaustedNow {
		log.Warnf(Category, "[SOCKS5 TCP] proxy leg diagnostics suppressed after %d records", proxyLegNoticeLimit)
	}
}

// routineStreamEnd reports a stream end the structured proxy-leg or tunnel
// records already cover, so the dependency's own line can stay at debug.
func routineStreamEnd(err error) bool {
	switch common.StreamErrorClass(err) {
	case "fin", "rst", "timeout", "closed":
		return true
	}
	return false
}

// routineUDPFailure keeps UDP failure classification unchanged: resets and
// closed sockets are routine; timeouts are not.
func routineUDPFailure(err error) bool {
	switch common.StreamErrorClass(err) {
	case "rst", "closed":
		return true
	}
	return false
}
