//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// attachConsole is a no-op.  The GUI binary is built with -H windowsgui so
// there is no console to attach to.  Log output goes to the log file and the
// in-app log viewer instead.
func attachConsole() {}

// ensureElevated checks if the current process is running with administrator
// privileges. If not, it re-launches itself via ShellExecuteW with the "runas"
// verb which triggers the UAC prompt, then exits the current (non-elevated)
// process. If already elevated this is a no-op.
func ensureElevated() {
	if isAdmin() {
		return
	}

	exe, err := os.Executable()
	if err != nil {
		return // best effort
	}

	args := strings.Join(os.Args[1:], " ")

	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	exePtr, _ := syscall.UTF16PtrFromString(exe)
	argPtr, _ := syscall.UTF16PtrFromString(args)

	err = windows.ShellExecute(0, verbPtr, exePtr, argPtr, nil, windows.SW_NORMAL)
	if err != nil {
		// User probably declined UAC or it's disabled. Continue unprivileged.
		return
	}

	os.Exit(0)
}

// isAdmin returns true when the current process token has the built-in
// Administrators group enabled.
func isAdmin() bool {
	var sid *windows.SID
	err := windows.AllocateAndInitializeSid(
		&windows.SECURITY_NT_AUTHORITY,
		2,
		windows.SECURITY_BUILTIN_DOMAIN_RID,
		windows.DOMAIN_ALIAS_RID_ADMINS,
		0, 0, 0, 0, 0, 0,
		&sid,
	)
	if err != nil {
		return false
	}
	defer windows.FreeSid(sid)

	member, err := windows.Token(0).IsMember(sid)
	if err != nil {
		return false
	}
	return member
}
