//go:build !darwin

package main

import "os"

// machineMemory returns the memory in bytes that the process can use, or 0
// when it can't be read.
func machineMemory() uint64 {
	return linuxMemory(os.ReadFile)
}
