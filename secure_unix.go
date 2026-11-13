//go:build !windows

package main

import "os"

func secureDir(path string) error { return os.Chmod(path, 0700) }
