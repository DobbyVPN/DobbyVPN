package tunnel

import (
	"sync"
	"sync/atomic"
	"time"

	"core/log"
)

// sessionTrafficLogInterval is the periodic totals cadence; a variable so
// tests can shorten it.
var sessionTrafficLogInterval = 30 * time.Second

// sessionTraffic accumulates one generation's proxied bytes so a maintainer
// can correlate a failure with transfer volume.
type sessionTraffic struct {
	up, down atomic.Int64
	started  time.Time
	done     chan struct{}
	stopOnce sync.Once
}

// logSessionTraffic writes a periodic totals record every interval once the
// session has carried any byte; unchanged values are written too, because a
// flat pair of records is the throughput-drop signal.
func (p *DobbyProxy) logSessionTraffic() {
	ticker := time.NewTicker(sessionTrafficLogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.session.done:
			return
		case <-ticker.C:
			up, down := p.session.up.Load(), p.session.down.Load()
			if up+down == 0 {
				continue
			}
			log.Infof(Category, "[Router] session traffic generation=%d bytesUp=%d bytesDown=%d", p.generation, up, down)
		}
	}
}

// closeSessionTraffic stops the periodic records and writes the final totals
// once. It is safe to call from both detach and stop.
func (p *DobbyProxy) closeSessionTraffic() {
	if p == nil || p.session.done == nil {
		// A hand-built proxy (tests without attach) has no session to close.
		return
	}
	p.session.stopOnce.Do(func() {
		close(p.session.done)
		log.Infof(Category, "[Router] session traffic end generation=%d bytesUp=%d bytesDown=%d duration=%s",
			p.generation, p.session.up.Load(), p.session.down.Load(), time.Since(p.session.started))
	})
}
