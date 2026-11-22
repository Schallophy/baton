//go:build !windows

package app

import "syscall"

// detachAttr has no effect outside Windows: the background start uses
// `setsid`-style detachment through the shell there.
func detachAttr() *syscall.SysProcAttr {
	return nil
}
