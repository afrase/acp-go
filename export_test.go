package acp

import "time"

// SetDrainTime sets drainTime for a test and returns what puts it back.
func SetDrainTime(d time.Duration) (restore func()) {
	old := drainTime
	drainTime = d
	return func() { drainTime = old }
}

// MaxOwed is how many answers may wait to be written before the next
// request waits.
const MaxOwed = maxOwed
