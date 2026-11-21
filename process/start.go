// Package process starts a program on pipes of its own and ends it with
// what it started, as [acp.Start] runs an agent. An agent that runs a CLI
// can run it the same way.
//
// On Unix, [OwnGroup] puts the program in a process group of its own,
// which [Group] signals and kills. On Windows the program starts a console
// process group of its own, for Ctrl+C, and [NewGroup] puts it in a job
// object, so Kill, and this process's end, end what it started too. On
// other systems a Group is the program alone.
//
// [acp.Start]: https://pkg.go.dev/github.com/afrase/acp-go#Start
package process

import (
	"errors"
	"os"
	"os/exec"
)

// Pipes are this process's ends of a started program's stdin, stdout and,
// when asked for, stderr (else nil).
type Pipes struct{ Stdin, Stdout, Stderr *os.File }

// Start starts cmd on pipes of its own for its stdin and stdout, and for
// its stderr if stderr is true. They are pipes Start makes, not cmd's
// StdinPipe and StdoutPipe: Wait would close those as the program exits,
// under a reader that may not yet have read what it wrote last. The
// caller closes them. cmd's Stdin and Stdout, and its Stderr if stderr is
// true, must be unset; when the start fails they are unset again, and the
// pipes closed.
func Start(cmd *exec.Cmd, stderr bool) (*Pipes, error) {
	if cmd.Stdin != nil || cmd.Stdout != nil || stderr && cmd.Stderr != nil {
		return nil, errors.New("process: Start sets the command's pipes itself")
	}
	var ours, theirs []*os.File
	closeAll := func() {
		for _, f := range append(ours, theirs...) {
			_ = f.Close()
		}
	}
	n := 2
	if stderr {
		n = 3
	}
	for i := range n {
		r, w, err := os.Pipe()
		if err != nil {
			closeAll()
			return nil, err
		}
		if i == 0 { // stdin: the program reads it
			r, w = w, r
		}
		ours, theirs = append(ours, r), append(theirs, w)
	}
	cmd.Stdin, cmd.Stdout = theirs[0], theirs[1]
	if stderr {
		cmd.Stderr = theirs[2]
	}
	if err := cmd.Start(); err != nil {
		closeAll()
		cmd.Stdin, cmd.Stdout = nil, nil
		if stderr {
			cmd.Stderr = nil
		}
		return nil, err
	}
	for _, f := range theirs {
		_ = f.Close() // the program has its own copies
	}
	p := &Pipes{Stdin: ours[0], Stdout: ours[1]}
	if stderr {
		p.Stderr = ours[2]
	}
	return p, nil
}
