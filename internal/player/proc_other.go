//go:build !linux

package player

import "os/exec"

func setProcAttr(cmd *exec.Cmd) {}
