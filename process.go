package acp

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Start runs an agent's command and speaks ACP to it over its stdin and
// stdout, handing what the agent sends to client. cmd's Stdin and Stdout
// must be unset; its Stderr is left as it is (nil discards the agent's
// logs). On Unix the agent runs in a process group of its own, so Close
// kills the processes it started too (an agent run through a launcher,
// such as npx, is a child of the launcher).
//
// The connection ends when the agent closes its stdout, or exits: what it
// wrote before exiting is still read, for up to a second (a process it
// left running may hold its stdout open). Done is closed once its process
// has been waited for; cmd.ProcessState then says how it ended. If cmd's
// WaitDelay is zero, Start sets it to a second.
func Start(cmd *exec.Cmd, client Client, opts ...Option) (*AgentConn, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil {
		return nil, errors.New("acp: Start sets the command's stdin and stdout itself")
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = inR, outW
	ownGroup(cmd)
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = time.Second
	}
	if err := cmd.Start(); err != nil {
		for _, f := range []*os.File{inR, inW, outR, outW} {
			_ = f.Close()
		}
		cmd.Stdin, cmd.Stdout = nil, nil
		return nil, err
	}
	_ = inR.Close() // the agent's ends
	_ = outW.Close()

	var once sync.Once
	closeFn := func(cause error) error {
		once.Do(func() { _ = inW.Close() })
		if cause == nil {
			_ = kill(cmd) // Close: now
		}
		return outR.Close()
	}
	a := &AgentConn{c: newConn(outR, inW, closeFn, newOptions(opts))}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait() // its result is in cmd.ProcessState
		close(exited)
	}()
	waited := make(chan struct{})
	go func() {
		select {
		case <-exited: // what it wrote last is still read, unless a process it left holds its stdout
			select {
			case <-a.c.ctx.Done():
			case <-time.After(drainTime):
				_ = outR.Close()
			}
		case <-a.c.ctx.Done(): // it closed its stdout, or a write failed: it gets drainTime to exit
			select {
			case <-exited:
			case <-time.After(drainTime):
			}
		}
		_ = kill(cmd) // what it left running, or itself
		<-exited
		close(waited)
	}()
	a.c.wait = func() { <-waited }
	a.c.start(clientHandler(client))
	return a, nil
}

// drainTime is how long what an exited agent wrote is still read, and how
// long an agent that closed its stdout has to exit before it is killed.
var drainTime = time.Second
