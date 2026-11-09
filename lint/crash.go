package lint

import (
	"fmt"
	"runtime/debug"
	"strings"
)

// CrashError is a panic in slop-police, with the rule and the file it
// happened in when known.
type CrashError struct {
	Rule  string
	File  string
	Value any
	Stack string
}

func (e *CrashError) Error() string {
	var where strings.Builder
	if e.Rule != "" {
		fmt.Fprintf(&where, " in %s", e.Rule)
	}
	if e.File != "" {
		fmt.Fprintf(&where, " on %s", e.File)
	}
	return fmt.Sprintf("slop-police crashed%s: %v\n\nPlease report it at https://github.com/kubk/slop-police/issues with this output.\n\n%s", where.String(), e.Value, e.Stack)
}

// asCrash returns the recovered value r with the rule and file filled in
// where they aren't known yet. The stack is the panicking goroutine's, so
// asCrash must be called from the deferred function that recovered r.
func asCrash(r any, rule string, file *File) *CrashError {
	crash, ok := r.(*CrashError)
	if !ok {
		crash = &CrashError{Value: r, Stack: string(debug.Stack())}
	}
	if crash.Rule == "" {
		crash.Rule = rule
	}
	if crash.File == "" && file != nil {
		crash.File = file.RelPath
	}
	return crash
}

// rethrow adds the rule and file to a panic passing through. Use it as
// `defer rethrow(rule, file)`.
func rethrow(rule string, file *File) {
	if r := recover(); r != nil {
		panic(asCrash(r, rule, file))
	}
}

// recoverCrash turns a panic into a *CrashError in *err. Use it as
// `defer recoverCrash(&err)`.
func recoverCrash(err *error) {
	if r := recover(); r != nil {
		*err = asCrash(r, "", nil)
	}
}
