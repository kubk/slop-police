package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
)

// startProfile starts a CPU profile when SLOP_POLICE_PROFILE names a
// directory, and returns the function that ends the run's profiles: it
// writes cpu.pprof and heap.pprof (what is live at the end, and everything
// that was allocated) there, for go tool pprof.
func startProfile() (stop func(), err error) {
	dir := os.Getenv("SLOP_POLICE_PROFILE")
	if dir == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cpu, err := os.Create(filepath.Join(dir, "cpu.pprof"))
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(cpu); err != nil {
		return nil, err
	}
	return func() {
		pprof.StopCPUProfile()
		cpu.Close()
		heap, err := os.Create(filepath.Join(dir, "heap.pprof"))
		if err == nil {
			// A heap profile is as of the last collection.
			runtime.GC()
			err = pprof.Lookup("heap").WriteTo(heap, 0)
			heap.Close()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}, nil
}
