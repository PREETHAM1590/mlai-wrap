//go:build windows

package main

// proc_windows.go — Windows has no process groups here; taskkill /T walks the child tree.

import (
	"os/exec"
	"strconv"
	"syscall"
)

func setProcGroup(cmd *exec.Cmd) {}

// createNoWindow is CREATE_NO_WINDOW: the child gets a console that is never shown.
const createNoWindow = 0x08000000

// hideConsole starts a short internal helper (mlai whoami, --version, agents
// stop, taskkill) without a console window. Not for interactive clients.
func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
	cmd.SysProcAttr.HideWindow = true
}

// junctionCommand runs mklink /J with the command line from junctionCmdLine,
// passed to CreateProcess unchanged.
func junctionCommand(link, target string) (*exec.Cmd, error) {
	line, err := junctionCmdLine(link, target)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
	hideConsole(cmd)
	return cmd, nil
}

// killTree kills cmd and its descendants.
func killTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	tk := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	hideConsole(tk)
	tk.Run()
	return cmd.Process.Kill()
}
