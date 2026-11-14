//go:build unix

package acp

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// interruptible returns f, or, when f is a pipe or socket in blocking mode
// (an agent's stdin and stdout usually are), a non-blocking duplicate of
// it: Go can't interrupt a Read or Write under way on a blocking file, so
// Close wouldn't end the connection while the other side is silent (or
// doesn't read). Terminals and regular files are left as they are.
func interruptible(f *os.File) io.ReadWriteCloser {
	if !errors.Is(f.SetReadDeadline(time.Time{}), os.ErrNoDeadline) {
		return f // it can be interrupted already
	}
	fi, err := f.Stat()
	if err != nil || fi.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return f
	}
	sc, err := f.SyscallConn()
	if err != nil {
		return f
	}
	dup := -1
	_ = sc.Control(func(fd uintptr) {
		syscall.ForkLock.RLock() // no child may inherit the duplicate
		defer syscall.ForkLock.RUnlock()
		if d, err := syscall.Dup(int(fd)); err == nil {
			syscall.CloseOnExec(d)
			dup = d
		}
	})
	if dup < 0 {
		return f
	}
	if syscall.SetNonblock(dup, true) != nil {
		_ = syscall.Close(dup)
		return f
	}
	return &dupFile{File: os.NewFile(uintptr(dup), f.Name()), orig: f}
}

// dupFile is a file read and written through a non-blocking duplicate.
// The two share their open file (its non-blocking mode too), so Close
// puts the mode back and closes both.
type dupFile struct {
	*os.File
	orig *os.File
}

func (d *dupFile) Close() error {
	err := d.File.Close() // ends a Read or Write under way
	if sc, cerr := d.orig.SyscallConn(); cerr == nil {
		_ = sc.Control(func(fd uintptr) { _ = syscall.SetNonblock(int(fd), false) })
	}
	if cerr := d.orig.Close(); err == nil {
		err = cerr
	}
	return err
}
