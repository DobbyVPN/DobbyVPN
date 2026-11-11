package tunnel

import (
	"fmt"
	"sync/atomic"
	"time"
)

// trafficCounter counts the bytes a connection moved in each direction and
// when the first byte came back, so a slow connection can be told from one
// that never carried data.
type trafficCounter struct {
	up, down   atomic.Int64
	firstByte  atomic.Pointer[time.Duration] // elapsed to first downstream byte, nil if none
	nowForTest func() time.Time
	readError  atomic.Bool
	writeError atomic.Bool
}

func (t *trafficCounter) now() time.Time {
	if t.nowForTest != nil {
		return t.nowForTest()
	}
	return time.Now()
}

func (t *trafficCounter) recordRead(n int, started time.Time) (time.Duration, bool) {
	if n <= 0 {
		return 0, false
	}
	t.down.Add(int64(n))
	if t.firstByte.Load() == nil {
		elapsed := t.now().Sub(started)
		if t.firstByte.CompareAndSwap(nil, &elapsed) {
			return elapsed, true
		}
	}
	return 0, false
}

func (t *trafficCounter) recordWrite(n int) {
	if n > 0 {
		t.up.Add(int64(n))
	}
}

// summary describes the traffic as fixed key=value fields. ttfb is measured
// from connection start and reads "none" when no data ever arrived.
func (t *trafficCounter) summary() string {
	ttfb := "none"
	if first := t.firstByte.Load(); first != nil {
		ttfb = first.Round(time.Millisecond).String()
	}
	return fmt.Sprintf("bytesUp=%d bytesDown=%d ttfb=%s", t.up.Load(), t.down.Load(), ttfb)
}
