//go:build !windows

package sys

import (
	"os/exec"
	"syscall"
)

func ApplyDetachSysProcAttr(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setsid = true
}
