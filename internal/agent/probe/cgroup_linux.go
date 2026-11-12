//go:build linux

package probe

import "time"

func init() {
	Register("cgroup", func(cfg map[string]any, env Env) (Probe, error) { return newCgroup(cfg, time.Now) })
}
