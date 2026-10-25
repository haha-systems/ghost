//go:build !darwin && !linux

package serve

import (
	"errors"
	"runtime"
)

// peerUID fails closed where Ghost has no peer credential check.
func peerUID(uintptr) (uint32, error) {
	return 0, errors.New("unix socket peer credentials are not supported on " + runtime.GOOS)
}
