//go:build windows

package main

// proc_windows.go — Windows has no process groups here; taskkill /T walks the child tree.

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
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

var procGetFinalPathNameByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// finalPath is the real location of an existing path as Windows sees it,
// with junctions and symlinks resolved (GetFinalPathNameByHandleW).
func finalPath(p string) (string, error) {
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	h, err := syscall.CreateFile(name, 0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 512)
	for {
		n, _, e := procGetFinalPathNameByHandle.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			return "", e
		}
		if int(n) < len(buf) {
			s := syscall.UTF16ToString(buf[:n])
			if strings.HasPrefix(s, `\\?\UNC\`) {
				return `\\` + s[len(`\\?\UNC\`):], nil
			}
			return strings.TrimPrefix(s, `\\?\`), nil
		}
		buf = make([]uint16, n+1)
	}
}

// physicalDir is dir with junctions and symlinks resolved: the deepest
// existing ancestor goes through finalPath (filepath.EvalSymlinks does not
// follow junctions) and the missing rest is appended. Only for the git check:
// it turns mapped drives into UNC paths, which mlai does not record.
func physicalDir(dir string) string {
	p := canonDir(dir)
	rest := ""
	for d := p; ; d = filepath.Dir(d) {
		if fp, err := finalPath(d); err == nil {
			return filepath.Join(fp, rest)
		}
		if d == filepath.Dir(d) {
			return p
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
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
