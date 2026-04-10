//go:build !windows

package main

// ensureElevated is a no-op on non-Windows platforms.
func ensureElevated() {}

// attachConsole is a no-op on non-Windows platforms.
func attachConsole() {}
