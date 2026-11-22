//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideConsole évite qu'une fenêtre noire s'affiche quand on lance PowerShell.
func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
