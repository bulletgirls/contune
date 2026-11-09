package sched

import "eum/internal/model"

// pendingKills는 host에 보낼 kill 명령을 꺼낸다. B4가 부르고 B7이 채운다. (stub: nil)
func (s *Scheduler) pendingKills(host string) []model.Command { return nil }
