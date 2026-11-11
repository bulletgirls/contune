//go:build !linux

package crash

func readSettings() (Settings, error) { return Settings{}, ErrNotImplemented }
