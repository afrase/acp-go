//go:build !unix

package acp

import (
	"io"
	"os"
)

// interruptible returns f: Close interrupts its I/O, or can't be made to.
func interruptible(f *os.File) io.ReadWriteCloser { return f }
