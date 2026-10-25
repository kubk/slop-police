package lint

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Timing prints phase durations to stderr when SLOP_POLICE_TIMING=1. A nil
// Timing prints nothing.
type Timing struct {
	out   io.Writer
	start time.Time
	last  time.Time
	mu    sync.Mutex
}

// NewTiming returns a Timing that prints to out, or nil when out is nil.
func NewTiming(out io.Writer) *Timing {
	if out == nil {
		return nil
	}
	now := time.Now()
	return &Timing{out: out, start: now, last: now}
}

// Mark prints the time since the previous mark.
func (t *Timing) Mark(name string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	fmt.Fprintf(t.out, "%-36s %8.1fms  (total %7.1fms)\n", name, ms(now.Sub(t.last)), ms(now.Sub(t.start)))
	t.last = now
}

// Span prints a duration measured by the caller.
func (t *Timing) Span(name string, d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fmt.Fprintf(t.out, "  %-34s %8.1fms\n", name, ms(d))
}

// Phase returns a function that prints the time since its previous call.
func (t *Timing) Phase(group string) func(name string) {
	last := time.Now()
	return func(name string) {
		now := time.Now()
		t.Span(group+": "+name, now.Sub(last))
		last = now
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
