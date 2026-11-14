package acp

import "time"

// SetDrainTime sets drainTime for a test and returns what puts it back.
func SetDrainTime(d time.Duration) (restore func()) {
	old := drainTime
	drainTime = d
	return func() { drainTime = old }
}
