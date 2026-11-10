package main

import "golang.org/x/sys/unix"

// machineMemory returns the memory in bytes that the process can use, or 0
// when it can't be read.
func machineMemory() uint64 {
	memory, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return memory
}
