package process_test

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/afrase/acp-go/process"
)

// Start's pipes are the program's stdin, stdout and stderr; a failed
// start leaves the command as it was.
func TestStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	cmd := exec.Command("sh", "-c", "read line; echo out $line; echo err >&2")
	p, err := process.Start(cmd, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Stdin.WriteString("in\n"); err != nil {
		t.Fatal(err)
	}
	_ = p.Stdin.Close()
	out, _ := io.ReadAll(p.Stdout)
	errOut, _ := io.ReadAll(p.Stderr)
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if string(out) != "out in\n" || string(errOut) != "err\n" {
		t.Errorf("stdout %q, stderr %q", out, errOut)
	}
	_ = p.Stdout.Close()
	_ = p.Stderr.Close()

	cmd = exec.Command("/no/such/program")
	if _, err := process.Start(cmd, false); err == nil || cmd.Stdin != nil || cmd.Stdout != nil {
		t.Errorf("a missing program: %v, stdin %v, stdout %v", err, cmd.Stdin, cmd.Stdout)
	}
	cmd = exec.Command("true")
	cmd.Stderr = os.Stderr
	if _, err := process.Start(cmd, true); err == nil {
		t.Error("Start took a command with its own stderr")
	}
}

// signalling is a pipe whose Read says it has started.
type signalling struct {
	io.ReadCloser
	reading chan struct{}
}

func (s signalling) Read(p []byte) (int, error) {
	close(s.reading)
	return s.ReadCloser.Read(p)
}

// Drain closes a pipe whose reader waits on it through a tick of idle
// time, and leaves one whose reader is behind until the reading ends.
func TestDrain(t *testing.T) {
	ticks := make(chan time.Time)
	calls := make(chan struct{})
	after := func(time.Duration) <-chan time.Time {
		calls <- struct{}{}
		return ticks
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	reading := make(chan struct{})
	waiting := &process.Watched{ReadCloser: signalling{r, reading}}
	readErr := make(chan error, 1)
	go func() {
		_, err := waiting.Read(make([]byte, 1))
		readErr <- err
	}()
	<-reading
	closed := make(chan bool, 1)
	go func() { closed <- waiting.Drain(nil, time.Second, after) }()
	<-calls
	ticks <- time.Time{}
	if !<-closed {
		t.Error("a pipe whose reader waited wasn't closed")
	}
	if err := <-readErr; !errors.Is(err, os.ErrClosed) {
		t.Errorf("the read: %v", err)
	}

	behind := &process.Watched{ReadCloser: io.NopCloser(nil)}
	ended := make(chan struct{})
	go func() { closed <- behind.Drain(ended, time.Second, after) }()
	<-calls
	ticks <- time.Time{}
	<-calls // no Read under way: it waits on
	close(ended)
	if <-closed {
		t.Error("a pipe whose reader is behind was closed")
	}
}
