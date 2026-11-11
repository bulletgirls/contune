//go:build !linux

package crash

func raiseLimit(pid int, bytes uint64) (string, error) { return "", nil }
