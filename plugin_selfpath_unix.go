//go:build cgo && !windows

package main

/*
// Linux needs _GNU_SOURCE for Dl_info/dladdr; some toolchains also need -ldl.
#cgo linux CFLAGS: -D_GNU_SOURCE
#cgo linux LDFLAGS: -ldl
#include <dlfcn.h>
#include <stddef.h>

// plugin_self_path returns the filesystem path of the shared object that
// contains this function (i.e. the plugin .so / .dylib). The returned pointer
// is owned by the dynamic linker and must not be freed.
//
// _GNU_SOURCE is required on Linux so that Dl_info / dladdr are visible
// (they are GNU extensions; Apple's dlfcn.h always exposes them).
static const char *plugin_self_path(void) {
	Dl_info info;
	if (dladdr((void *)plugin_self_path, &info) == 0 || info.dli_fname == NULL) {
		return NULL;
	}
	return info.dli_fname;
}
*/
import "C"

import "path/filepath"

func selfSharedLibraryPathPlatform() string {
	p := C.plugin_self_path()
	if p == nil {
		return ""
	}
	path := C.GoString(p)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}
