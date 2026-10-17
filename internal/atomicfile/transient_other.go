//go:build !windows

package atomicfile

// transient is false everywhere but Windows: a rename over an open file
// succeeds on Unix.
func transient(error) bool { return false }
