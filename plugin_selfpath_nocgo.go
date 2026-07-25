//go:build !cgo && !windows

package main

// Without cgo on Unix we cannot call dladdr; leave empty so defaultStatePath
// falls back to <executable>/plugins/<goos>/<goarch>/.
func selfSharedLibraryPathPlatform() string { return "" }
