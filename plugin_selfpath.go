package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// selfLibraryPathForTest overrides the resolved shared-library path in tests.
var selfLibraryPathForTest string

// selfSharedLibraryPath returns the absolute path of this plugin's loaded
// dynamic library (.so/.dll/.dylib), or empty if it cannot be determined.
func selfSharedLibraryPath() string {
	if selfLibraryPathForTest != "" {
		return selfLibraryPathForTest
	}
	return selfSharedLibraryPathPlatform()
}

// isPluginShadowDir reports whether dir is a CPA Windows shadow-copy directory
// under $TMP/cliproxy-pluginhost/... (see CLIProxyAPI pluginhost loader).
// State must not live there — it is process-temp and not the deploy plugins tree.
func isPluginShadowDir(dir string) bool {
	dir = filepath.Clean(dir)
	tmp := filepath.Clean(os.TempDir())
	marker := filepath.Join(tmp, "cliproxy-pluginhost")
	return dir == marker || strings.HasPrefix(dir, marker+string(os.PathSeparator))
}

// defaultStatePath picks where state_file goes when config is empty:
//  1. Directory of this plugin library (next to .so/.dll), unless it's a Windows shadow temp dir
//  2. <dir(executable)>/plugins/<goos>/<goarch>/ (standard CPA deploy layout)
//  3. Process cwd + defaultStateFile (last resort)
func defaultStatePath() (string, error) {
	if self := selfSharedLibraryPath(); self != "" {
		dir := filepath.Dir(self)
		if !isPluginShadowDir(dir) {
			return filepath.Join(dir, defaultStateFile), nil
		}
	}
	if exe, err := os.Executable(); err == nil {
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			// still use the unresolved path
			exe, _ = os.Executable()
		}
		candidate := filepath.Join(filepath.Dir(exe), "plugins", runtime.GOOS, runtime.GOARCH, defaultStateFile)
		return candidate, nil
	}
	return filepath.Abs(defaultStateFile)
}
