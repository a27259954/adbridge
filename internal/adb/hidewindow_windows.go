package adb

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// HideWindow stops a console window from flashing when the GUI app spawns adb.
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
