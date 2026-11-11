//go:build linux

package crash

import (
	"os"
	"strings"
)

func readSettings() (Settings, error) {
	b, err := os.ReadFile("/proc/sys/kernel/core_pattern")
	if err != nil {
		return Settings{}, err
	}
	s := Settings{Pattern: strings.TrimRight(string(b), "\n")}
	if u, err := os.ReadFile("/proc/sys/kernel/core_uses_pid"); err == nil {
		s.UsesPID = strings.TrimSpace(string(u)) != "0"
	}
	return s, nil
}
