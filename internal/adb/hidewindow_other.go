//go:build !windows

package adb

import "os/exec"

// HideWindow is a no-op off Windows.
func HideWindow(*exec.Cmd) {}
