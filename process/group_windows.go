//go:build windows

package process

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// OwnGroup has cmd start a console process group of its own, so the
// console's Ctrl+C reaches this process alone (it may cancel a turn on it,
// and the program would die of it), as a Unix process group keeps a
// terminal's SIGINT from the program.
func OwnGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// Group is a started program in a job object, which the processes it
// starts join, so Kill ends them all; the job also kills them if this
// process dies. A process the program starts before it joins the job (a
// moment after it starts) is left, as is one it starts to break away
// from the job (CREATE_BREAKAWAY_FROM_JOB), such as a browser it opens.
// Without a job (the system refused one), Kill kills the program alone.
type Group struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	job syscall.Handle // 0 when there is none, or once released
}

// NewGroup puts cmd, which has started, in a job object of its own.
func NewGroup(cmd *exec.Cmd) *Group {
	g := &Group{cmd: cmd}
	job, err := newKillJob()
	if err != nil {
		return g
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid)) //nolint:gosec // a pid fits
	if err != nil {
		_ = syscall.CloseHandle(job)
		return g
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	if r, _, _ := procAssignProcessToJobObject.Call(uintptr(job), uintptr(h)); r == 0 {
		_ = syscall.CloseHandle(job)
		return g
	}
	g.job = job
	return g
}

// Kill ends the program's job, or the program alone when it has none.
func (g *Group) Kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		if r, _, _ := procTerminateJobObject.Call(uintptr(g.job), 1); r != 0 {
			return nil
		}
	}
	return g.cmd.Process.Kill()
}

// Signal ends the job for os.Kill, as Kill does; any other signal goes to
// the program alone (Windows delivers no others).
func (g *Group) Signal(sig os.Signal) error {
	if sig == os.Kill {
		return g.Kill()
	}
	return g.cmd.Process.Signal(sig)
}

// Sweep ends what is left in the program's job once the program has
// exited.
func (g *Group) Sweep() error { return g.Kill() }

// Release closes the job, once the program has been waited for; what is
// still in it is killed then.
func (g *Group) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = syscall.CloseHandle(g.job)
		g.job = 0
	}
}

// ExitSignal is nil: Windows doesn't say a signal ended a program.
func ExitSignal(error) os.Signal { return nil }

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

const (
	processTerminate                  = 0x0001
	processSetQuota                   = 0x0100
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitBreakawayOK         = 0x0800 // JOB_OBJECT_LIMIT_BREAKAWAY_OK
	jobObjectLimitKillOnJobClose      = 0x2000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
)

// jobExtendedLimit is JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobExtendedLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	IoInfo                  [6]uint64 // IO_COUNTERS
	ProcessMemoryLimit      uintptr
	JobMemoryLimit          uintptr
	PeakProcessMemoryUsed   uintptr
	PeakJobMemoryUsed       uintptr
}

// newKillJob makes a job object whose processes are killed when it is
// closed, and which a process may leave by starting with
// CREATE_BREAKAWAY_FROM_JOB.
func newKillJob() (syscall.Handle, error) {
	r, _, err := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return 0, err
	}
	job := syscall.Handle(r)
	info := jobExtendedLimit{LimitFlags: jobObjectLimitKillOnJobClose | jobObjectLimitBreakawayOK}
	r, _, err = procSetInformationJobObject.Call(uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)) //nolint:gosec // Proc.Call keeps the pointer alive (go:uintptrescapes)
	if r == 0 {
		_ = syscall.CloseHandle(job)
		return 0, err
	}
	return job, nil
}
