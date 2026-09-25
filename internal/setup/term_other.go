//go:build !linux && !darwin && !windows

package setup

// hideStdinEcho is a no-op fallback for platforms without terminal echo
// control. Echoing can't be disabled there; callers proceed normally.
func hideStdinEcho() (func(), error) {
	return func() {}, nil
}
