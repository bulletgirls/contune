// Package store는 매니저 저장소 확장 지점이다(02 §7, 05 §12). 지표·프로파일·로그는 MetricStore, 실행 상태는 StateStore.
package store

import (
	"context"
	"errors"

	"eum/internal/model"
)

// ErrNotFound는 찾는 행이 없을 때의 에러다.
var ErrNotFound = errors.New("not found")

// MetricStore는 지표·함수 프로파일·업무 로그 저장소다(SQLite → TSDB / MySQL).
type MetricStore interface {
	Write(ctx context.Context, batch []model.Sample) error
	Query(ctx context.Context, q model.SeriesQuery) ([]model.Series, error)
	WriteProfiles(ctx context.Context, ps []model.Profile) error
	QueryProfile(ctx context.Context, execID string, from, to int64) (model.Profile, error) // 구간 합산
	WriteLogs(ctx context.Context, evs []model.LogEvent) error
	QueryLogs(ctx context.Context, q model.LogQuery) ([]model.LogEvent, error) // 최신순
}

// StateStore는 실행 상태 저장소다(무결성 우선). 상태를 바꾸는 길은 CreateRuns와 Transition 둘뿐이다.
type StateStore interface {
	// 새 실행 행(WAITING)을 만들고 각 행에 생성 이벤트(seq 1, from '' → WAITING)를 남긴다. 한 트랜잭션.
	CreateRuns(ctx context.Context, runs []model.Run, actor, reason string) error
	// 비교 후 교체. 현재 상태가 t.From 중 하나일 때만 t.To와 t.Patch를 반영하고 run_events에 다음 seq로 남긴다.
	// 반영했으면 true. 상태가 맞지 않거나(중복·늦은 이벤트) exec_id가 없으면 false, 에러 아님.
	Transition(ctx context.Context, t model.Transition) (bool, error)
	GetRun(ctx context.Context, execID string) (model.Run, []model.RunEvent, error) // 없으면 ErrNotFound
	ListRuns(ctx context.Context, f model.RunFilter) ([]model.Run, error)           // scheduled_at 내림차순
	UpsertHost(ctx context.Context, h model.Host) error                             // 새 호스트, 그룹 변경, 30초마다만 호출됨
	ListHosts(ctx context.Context) ([]model.Host, error)

	CreateIncident(ctx context.Context, in model.Incident) (int64, error)
	UpdateIncident(ctx context.Context, in model.Incident) error                 // ID로, 없으면 ErrNotFound
	ListIncidents(ctx context.Context, from, to int64) ([]model.Incident, error) // 구간과 겹침, start 내림차순
	AddRiskHold(ctx context.Context, h model.RiskHold) (int64, error)
	EndRiskHold(ctx context.Context, id, ts int64, by, note string) error        // 없거나 이미 끝났으면 ErrNotFound
	ListRiskHolds(ctx context.Context, from, to int64) ([]model.RiskHold, error) // [at, ended_at(0이면 ∞)]가 [from,to)와 겹침(to 0이면 열림), at 내림차순
	AddAlert(ctx context.Context, a model.Alert) (int64, error)
	ResolveAlert(ctx context.Context, id, ts int64) error
	AckAlert(ctx context.Context, id, ts int64, by string) error
	ListAlerts(ctx context.Context, f model.AlertFilter) ([]model.Alert, error)
	AddAudit(ctx context.Context, e model.AuditEntry) error
	ListAudit(ctx context.Context, q model.AuditQuery) ([]model.AuditEntry, error) // id 내림차순
	AuditStats(ctx context.Context, from, to int64) (model.AuditStats, error)      // ts [from,to) 안의 action·actor별 건수
	// 업무 관제 5분 칸(05 §19.5). (panel, ts)가 같으면 바꿔 쓴다.
	PutBizState(ctx context.Context, b model.BizState) error
	ListBizState(ctx context.Context, from, to int64) ([]model.BizState, error) // ts [from,to), ts 오름차순(같으면 panel 순)
}
