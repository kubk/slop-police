package main

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"time"

	"github.com/microsoft/typescript-go/x/slop-police/lint"
)

// unknownMemoryBudget is the memory budget on a machine whose memory can't
// be read.
const unknownMemoryBudget = 1536 << 20

// tuneRuntime sets the memory budget: SLOP_POLICE_MEMORY_GB gigabytes, or a
// quarter of the machine's memory. Up to it the run spends memory to be
// fast. The GC is off: most of the heap (ASTs, types) stays live for the
// whole short run, so collecting early mostly wastes CPU. And the projects
// keep their checkers (see lint.MemoryBudget). Once the heap is over the
// budget, the GC runs, and more eagerly than Go's default. GOGC and
// GOMEMLIMIT leave the GC to the Go runtime.
func tuneRuntime() error {
	budget, err := memoryBudget()
	if err != nil {
		return err
	}
	lint.MemoryBudget = budget
	if os.Getenv("GOGC") != "" || os.Getenv("GOMEMLIMIT") != "" {
		return nil
	}
	debug.SetGCPercent(-1)
	// Runs the GC anyway if the heap gets far over the budget before the
	// sampling below notices.
	debug.SetMemoryLimit(int64(2 * budget))
	go func() {
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for range time.Tick(5 * time.Millisecond) {
			if metrics.Read(sample); sample[0].Value.Uint64() > budget {
				debug.SetGCPercent(50)
				debug.SetMemoryLimit(math.MaxInt64)
				return
			}
		}
	}()
	return nil
}

// memoryBudget returns the memory budget in bytes, at most a quarter of what
// an int64 holds.
func memoryBudget() (uint64, error) {
	value := os.Getenv("SLOP_POLICE_MEMORY_GB")
	if value == "" {
		if memory := machineMemory(); memory > 0 {
			return min(memory/4, math.MaxInt64/4), nil
		}
		return unknownMemoryBudget, nil
	}
	gb, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(gb) || gb < 0 {
		return 0, fmt.Errorf("SLOP_POLICE_MEMORY_GB must be a number of gigabytes, got %q", value)
	}
	return uint64(min(gb*(1<<30), math.MaxInt64/4)), nil
}
