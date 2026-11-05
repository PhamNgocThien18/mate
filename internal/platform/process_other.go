//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package platform

import (
	"os/exec"
	"syscall"
)

// errNoSignal is Signal's and SignalGroup's answer here: nothing is sent,
// and the error is neither syscall.ESRCH nor syscall.EPERM.
const errNoSignal unsupported = "signalling a process is not supported"

// errNoGroup is StartNewGroup's: the command is not started, since nothing
// could reach the processes it starts.
const errNoGroup unsupported = "starting a command in a new process group is not supported"

// errNoSession is StartNewSession's: the command is not started, since it
// could not outlive this process's terminal.
const errNoSession unsupported = "starting a command in a new session is not supported"

func Signal(int, syscall.Signal) error { return errNoSignal }

func SignalGroup(int, syscall.Signal) error { return errNoSignal }

func StartNewGroup(*exec.Cmd) error { return errNoGroup }

func StartNewSession(*exec.Cmd) error { return errNoSession }
