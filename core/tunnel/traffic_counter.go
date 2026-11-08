package tunnel

import (
	"fmt"
	"sync/atomic"
	"time"
)

// trafficCounter counts the bytes a TCP connection moved in each direction and
// when the first byte came back, so a slow connection can be told from one
// that never carried data.
type trafficCounter struct {
	up, down   atomic.Int64
	firstByte  atomic.Int64 // unix nanoseconds of the first downstream byte, 0 if none
	nowForTest func() time.Time
}

func (t *trafficCounter) now() time.Time {
	if t.nowForTest != nil {
		return t.nowForTest()
	}
	return time.Now()
}

func (t *trafficCounter) recordRead(n int) {
	if n <= 0 {
		return
	}
	t.down.Add(int64(n))
	t.firstByte.CompareAndSwap(0, t.now().UnixNano())
}

func (t *trafficCounter) recordWrite(n int) {
	if n > 0 {
		t.up.Add(int64(n))
	}
}

// summary describes the traffic as fixed key=value fields. ttfb is measured
// from started and reads "none" when no data ever arrived.
func (t *trafficCounter) summary(started time.Time) string {
	ttfb := "none"
	if first := t.firstByte.Load(); first != 0 {
		ttfb = time.Unix(0, first).Sub(started).Round(time.Millisecond).String()
	}
	return fmt.Sprintf("bytesUp=%d bytesDown=%d ttfb=%s", t.up.Load(), t.down.Load(), ttfb)
}
