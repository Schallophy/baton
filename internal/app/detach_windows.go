//go:build windows

package app

import "syscall"

// detachAttr makes a spawned baton outlive its parent terminal.
//
// DETACHED_PROCESS: the child gets no console of its own, so closing the
// terminal window does not take it down. CREATE_NEW_PROCESS_GROUP lets Ctrl+C
// in the parent stay local instead of travelling to the child.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
		HideWindow:    true,
	}
}
