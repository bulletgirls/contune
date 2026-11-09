//go:build linux

package probe

import "time"

func init() {
	Register("procfs", func(cfg map[string]any, env Env) (Probe, error) { return newProcfs(cfg, time.Now) })
}
