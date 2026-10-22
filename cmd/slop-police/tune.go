package main

import (
	"math"
	"os"
	"runtime/debug"
	"runtime/metrics"
	"time"
)

// tuneRuntime turns the GC off until the heap reaches 1.5 GB. Most of the
// heap (ASTs, types) stays live for the whole short run, so collecting early
// mostly wastes CPU. Larger projects then get a normal GC. GOGC and
// GOMEMLIMIT override this.
func tuneRuntime() {
	if os.Getenv("GOGC") != "" || os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	debug.SetGCPercent(-1)
	debug.SetMemoryLimit(4 << 30)
	go func() {
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for range time.Tick(5 * time.Millisecond) {
			if metrics.Read(sample); sample[0].Value.Uint64() > 1536<<20 {
				debug.SetGCPercent(200)
				debug.SetMemoryLimit(math.MaxInt64)
				return
			}
		}
	}()
}
