package process

import (
	"io"
	"sync/atomic"
	"time"
)

// Watched is a program's stdout or stderr that tells whether its reader is
// waiting on it, so the reading can be given up once the program has
// exited and a process it left running holds the pipe open.
type Watched struct {
	io.ReadCloser
	edges atomic.Int64 // each Read's start and its end: odd while a Read is under way
}

func (w *Watched) Read(p []byte) (int, error) {
	w.edges.Add(1)
	defer w.edges.Add(1)
	return w.ReadCloser.Read(p)
}

// Drain waits for the reading to end (ended), or closes the pipe once a
// Read has waited idle with nothing to read, and reports whether it closed
// it. A reader that is behind, still handling what it read, isn't cut off:
// what the program wrote before it exited is still in the pipe. after is
// the clock (nil: [time.After]).
func (w *Watched) Drain(ended <-chan struct{}, idle time.Duration, after func(time.Duration) <-chan time.Time) bool {
	if after == nil {
		after = time.After
	}
	for {
		armed := w.edges.Load()
		select {
		case <-ended:
			return false
		case <-after(idle):
		}
		if armed%2 == 1 && w.edges.Load() == armed {
			_ = w.Close()
			return true
		}
	}
}
