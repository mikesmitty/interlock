//go:build windows

package atomicfile

import (
	"errors"

	"golang.org/x/sys/windows"
)

// transient reports whether a failed rename may succeed once another
// program lets go of the file.
func transient(err error) bool {
	var errno windows.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION:
		return true
	}
	return false
}
