// Package leaktest finds goroutines a test left running, as goleak does,
// without a dependency: the module imports only the standard library.
package leaktest

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// Check fails t, when it ends, if a goroutine of this module's code that
// wasn't running when Check was called is still running (after waiting up
// to five seconds for it to end).
func Check(t testing.TB) {
	t.Helper()
	before := map[string]bool{}
	for _, g := range goroutines() {
		before[g.id] = true
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(5 * time.Second)
		for {
			var left []string
			for _, g := range goroutines() {
				if !before[g.id] && g.ours() {
					left = append(left, g.stack)
				}
			}
			if len(left) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("%d goroutines left running:\n\n%s", len(left), strings.Join(left, "\n\n"))
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}

type goroutine struct{ id, stack string }

// ours reports whether the goroutine runs this module's code (or was
// started by it), and isn't a test's own goroutine or the checker's.
func (g goroutine) ours() bool {
	return strings.Contains(g.stack, "github.com/afrase/acp-go") &&
		!strings.Contains(g.stack, "testing.tRunner(") &&
		!strings.Contains(g.stack, "acp-go/internal/leaktest.")
}

func goroutines() []goroutine {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []goroutine
	for g := range strings.SplitSeq(string(buf), "\n\n") {
		head, _, _ := strings.Cut(g, "\n")
		fields := strings.Fields(head) // goroutine 7 [running]:
		if len(fields) < 2 {
			continue
		}
		out = append(out, goroutine{id: fields[1], stack: g})
	}
	return out
}
