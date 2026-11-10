package main

import (
	"os"
	"testing"
)

func TestLinuxMemory(t *testing.T) {
	t.Parallel()
	const meminfo = "MemTotal:       16303428 kB\nMemFree:         1203428 kB\nMemAvailable:    9203428 kB\n"
	const machine = 16303428 << 10
	tests := []struct {
		name  string
		files map[string]string
		want  uint64
	}{
		{"no files", nil, 0},
		{"machine", map[string]string{"/proc/meminfo": meminfo}, machine},
		{"cgroup v2 without a limit", map[string]string{"/proc/meminfo": meminfo, "/sys/fs/cgroup/memory.max": "max\n"}, machine},
		{"cgroup v2 limit", map[string]string{"/proc/meminfo": meminfo, "/sys/fs/cgroup/memory.max": "4294967296\n"}, 4 << 30},
		{"cgroup v1 without a limit", map[string]string{"/proc/meminfo": meminfo, "/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712\n"}, machine},
		{"cgroup v1 limit", map[string]string{"/proc/meminfo": meminfo, "/sys/fs/cgroup/memory/memory.limit_in_bytes": "2147483648\n"}, 2 << 30},
		{"limit over the machine's memory", map[string]string{"/proc/meminfo": meminfo, "/sys/fs/cgroup/memory.max": "68719476736\n"}, machine},
		{"limit only", map[string]string{"/sys/fs/cgroup/memory.max": "1073741824\n"}, 1 << 30},
	}
	for _, test := range tests {
		got := linuxMemory(func(path string) ([]byte, error) {
			content, ok := test.files[path]
			if !ok {
				return nil, os.ErrNotExist
			}
			return []byte(content), nil
		})
		if got != test.want {
			t.Errorf("%s: got %d, want %d", test.name, got, test.want)
		}
	}
}

func TestMemoryBudget(t *testing.T) {
	tests := []struct {
		value string
		want  uint64
		fails bool
	}{
		{"2", 2 << 30, false},
		{"0.5", 1 << 29, false},
		{"0", 0, false},
		{"-1", 0, true},
		{"4GB", 0, true},
		{"NaN", 0, true},
	}
	for _, test := range tests {
		t.Setenv("SLOP_POLICE_MEMORY_GB", test.value)
		got, err := memoryBudget()
		if got != test.want || (err != nil) != test.fails {
			t.Errorf("%q: got %d, %v, want %d, fails %v", test.value, got, err, test.want, test.fails)
		}
	}
	t.Setenv("SLOP_POLICE_MEMORY_GB", "")
	want := uint64(unknownMemoryBudget)
	if memory := machineMemory(); memory > 0 {
		want = memory / 4
	}
	if got, err := memoryBudget(); err != nil || got != want {
		t.Errorf("default: got %d, %v, want %d", got, err, want)
	}
}
