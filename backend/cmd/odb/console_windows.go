//go:build windows

package main

import "golang.org/x/sys/windows"

// The console prints odb's UTF-8 as is (·, —, names in any script) instead of through the
// legacy code page, in cmd and PowerShell alike (OC-186). Not a console: nothing to do.
func init() {
	_ = windows.SetConsoleOutputCP(65001) // CP_UTF8
}
