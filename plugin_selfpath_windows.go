//go:build windows

package main

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	modKernel32            = syscall.NewLazyDLL("kernel32.dll")
	procGetModuleHandleExW = modKernel32.NewProc("GetModuleHandleExW")
	procGetModuleFileNameW = modKernel32.NewProc("GetModuleFileNameW")
)

// pluginSelfAnchor lives in this DLL's data segment so GetModuleHandleEx can
// resolve the module from its address. Go does not allow &func, so a package
// variable is required (not the function itself).
var pluginSelfAnchor byte

const (
	getModuleHandleExFlagFromAddress       = 0x00000004
	getModuleHandleExFlagUnchangedRefcount = 0x00000002
)

// selfSharedLibraryPathPlatform resolves the path of the loaded module that
// contains this package (the plugin .dll). On CPA Windows hosts the result may
// be a process-local shadow copy under %TEMP%\cliproxy-pluginhost; callers must
// treat that via isPluginShadowDir and fall back to the deploy plugins tree.
func selfSharedLibraryPathPlatform() string {
	var handle syscall.Handle
	addr := uintptr(unsafe.Pointer(&pluginSelfAnchor))
	flags := uintptr(getModuleHandleExFlagFromAddress | getModuleHandleExFlagUnchangedRefcount)
	r1, _, _ := procGetModuleHandleExW.Call(flags, addr, uintptr(unsafe.Pointer(&handle)))
	if r1 == 0 || handle == 0 {
		return ""
	}
	buf := make([]uint16, syscall.MAX_PATH)
	n, _, _ := procGetModuleFileNameW.Call(uintptr(handle), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	path := syscall.UTF16ToString(buf[:n])
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
