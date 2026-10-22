//go:build linux

package player

import (
	"os/exec"
	"syscall"
)

// setProcAttr makes the kernel terminate mpv if pony itself dies, so a crash
// never leaves an orphaned player behind.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
