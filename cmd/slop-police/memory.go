package main

import (
	"bytes"
	"strconv"
)

// linuxMemory returns the memory in bytes that a process on Linux can use:
// the machine's, or the limit of the container it runs in when that is less.
// It reads the files with read, and returns 0 when they say nothing.
func linuxMemory(read func(path string) ([]byte, error)) uint64 {
	var memory uint64
	if data, err := read("/proc/meminfo"); err == nil {
		for line := range bytes.Lines(data) {
			if fields := bytes.Fields(line); len(fields) >= 2 && string(fields[0]) == "MemTotal:" {
				kb, _ := strconv.ParseUint(string(fields[1]), 10, 64)
				memory = kb << 10
			}
		}
	}
	// cgroup v2, then v1. Without a limit the files say "max" or a number
	// far over the machine's memory.
	for _, path := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		data, err := read(path)
		if err != nil {
			continue
		}
		if limit, err := strconv.ParseUint(string(bytes.TrimSpace(data)), 10, 64); err == nil && limit > 0 && (memory == 0 || limit < memory) {
			memory = limit
		}
	}
	return memory
}
