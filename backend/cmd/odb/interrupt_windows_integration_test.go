//go:build integration && windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// newProcessGroup starts cmd in its own console process group, so a Ctrl+Break reaches it and
// not the test.
func newProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// interrupt sends Ctrl+Break to cmd's process group: Go delivers it as os.Interrupt, like Ctrl+C
// (which a new process group ignores).
func interrupt(cmd *exec.Cmd) error {
	return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid)) //nolint:gosec // G115: a pid fits in uint32
}
