package query

import (
	"context"

	"eum/internal/manager/live"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// 사용자 보드(07, 카드 W)가 쓰는 조회 도우미. 동작은 /series·/panels와 같다.

// MergedHosts는 상태 저장소의 서버 목록과 live 캐시를 합친 것이다(/hosts·그룹 범위와 같은 목록).
func MergedHosts(ctx context.Context, st store.StateStore, lv *live.Cache) []model.Host {
	return mergedHosts(ctx, Deps{State: st, Live: lv})
}

// InGroup은 서버 그룹 group이 범위 g 안인가다(g가 비면 늘 참, "매매"는 "매매/체결"을 포함).
func InGroup(group, g string) bool { return g == "" || inGroup(group, g) }

// AutoStep은 /series의 기본 step(초)이다: 구간(ms)을 점 600개 안쪽으로.
func AutoStep(from, to int64) int { return autoStep(from, to) }

// AggregateSeries는 /series?agg=와 같다: 같은 (name, exec_id)의 서버별 계열을 시각마다 하나로(host "").
func AggregateSeries(ss []model.Series, agg string) []model.Series { return aggregateSeries(ss, agg) }

// ValidAgg는 sum | max | avg | min인가다.
func ValidAgg(a string) bool { return validAgg(a) }

// TileOut은 GET /panels가 내보내는 타일 JSON 하나다(정의 + 판정).
func TileOut(t model.Tile, s TileState) any {
	return tileOut{Tile: t, Value: s.Value, State: s.State, Reason: s.Reason, Off: s.Off,
		Host: s.Host, StaleSince: s.StaleSince, ExecID: s.ExecID, Service: s.Service, TS: s.TS}
}
