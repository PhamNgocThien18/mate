//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package platform

import (
	"os/exec"
	"syscall"
)

// Signal sends sig to the process pid. Signal 0 sends nothing and only
// checks the process: nil when it exists and this user may signal it. The
// error is kill(2)'s own, for the caller to read: syscall.ESRCH when no
// process has that pid, syscall.EPERM when one has but it belongs to someone
// else. A pid below 1 names no one process (kill(2) reads 0 as this
// process's group, -1 as every process the user may signal, -n as a group),
// so Signal refuses it with syscall.EINVAL and sends nothing.
func Signal(pid int, sig syscall.Signal) error {
	if pid < 1 {
		return syscall.EINVAL
	}
	return syscall.Kill(pid, sig)
}

// SignalGroup sends sig to every process in the process group pgid: the
// pid of a command StartNewGroup started, which leads the group. The error
// is kill(2)'s own, as for Signal. A pgid below 2 is not a group SignalGroup
// will signal (kill(2) reads -1 as every process the user may signal and 0
// as this process's group), so it is refused with syscall.EINVAL and nothing
// is sent.
func SignalGroup(pgid int, sig syscall.Signal) error {
	if pgid < 2 {
		return syscall.EINVAL
	}
	return syscall.Kill(-pgid, sig)
}

// StartNewGroup starts cmd as the leader of a new process group, whose id is
// cmd.Process.Pid: SignalGroup then reaches the command and every process it
// starts, and a signal sent to this process's own group does not reach
// them. It sets cmd.SysProcAttr, replacing what was there.
func StartNewGroup(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd.Start()
}

// StartNewSession starts cmd as the leader of a new session, and of a new
// process group in it, with no controlling terminal: the command outlives
// the terminal this process runs in, and neither a signal sent to this
// process's group nor the terminal's hangup reaches it. It sets
// cmd.SysProcAttr, replacing what was there.
func StartNewSession(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
