//go:build integration && !windows

package main

import (
	"os"
	"os/exec"
)

func newProcessGroup(*exec.Cmd) {}

// interrupt sends what Ctrl+C sends.
func interrupt(cmd *exec.Cmd) error { return cmd.Process.Signal(os.Interrupt) }
