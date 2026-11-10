// Package audit는 감사 기록을 남기는 함수와 조회 API(GET /api/v1/audit, /flows/{id}/history)를 둔다.
package audit

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"eum/internal/manager/actor"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Record는 요청한 사람(actor.Of(r))의 이름으로 감사 기록을 남긴다. st가 nil이면 아무것도 하지 않고,
// 실패해도 조작 자체는 막지 않는다(slog.Warn).
func Record(r *http.Request, st store.StateStore, now func() time.Time, action, target, detail string) {
	if st == nil {
		return
	}
	Write(r.Context(), st, now, actor.Of(r), action, target, detail)
}

// Write는 actor를 직접 받아 남긴다(로그인 실패처럼 사용자가 아직 없는 경우).
func Write(ctx context.Context, st store.StateStore, now func() time.Time, who, action, target, detail string) {
	if st == nil {
		return
	}
	e := model.AuditEntry{TS: now().UnixMilli(), Actor: who, Action: action, Target: target, Detail: detail}
	if err := st.AddAudit(ctx, e); err != nil {
		slog.Warn("감사 기록 실패", "action", action, "target", target, "err", err)
	}
}
