//go:build windows

package acp

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// ownGroup has cmd start a console process group of its own, so the
// console's Ctrl+C reaches the client alone (the client may cancel a turn
// on it, and the agent would die of it), as a Unix process group keeps a
// terminal's SIGINT from the agent.
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// group is a started agent in a job object, which the processes it
// starts join, so kill ends them all; the job also kills them if the
// client dies. A process the agent starts before it joins the job (a
// moment after it starts) is left. Without a job (the system refused
// one), kill kills the agent alone.
type group struct {
	cmd *exec.Cmd
	mu  sync.Mutex
	job syscall.Handle // 0 when there is none, or once released
}

func newGroup(cmd *exec.Cmd) *group {
	g := &group{cmd: cmd}
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

// kill ends the agent's job, or the agent alone when it has none.
func (g *group) kill() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		if r, _, _ := procTerminateJobObject.Call(uintptr(g.job), 1); r != 0 {
			return nil
		}
	}
	return g.cmd.Process.Kill()
}

// release closes the job, once the agent has been waited for; what is
// still in it is killed then.
func (g *group) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.job != 0 {
		_ = syscall.CloseHandle(g.job)
		g.job = 0
	}
}

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
	jobObjectLimitKillOnJobClose      = 0x2000
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
// closed.
func newKillJob() (syscall.Handle, error) {
	r, _, err := procCreateJobObjectW.Call(0, 0)
	if r == 0 {
		return 0, err
	}
	job := syscall.Handle(r)
	info := jobExtendedLimit{LimitFlags: jobObjectLimitKillOnJobClose}
	r, _, err = procSetInformationJobObject.Call(uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)) //nolint:gosec // Proc.Call keeps the pointer alive (go:uintptrescapes)
	if r == 0 {
		_ = syscall.CloseHandle(job)
		return 0, err
	}
	return job, nil
}
