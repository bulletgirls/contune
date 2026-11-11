// Package dash는 사용자 보드다(07, 카드 W): 보드 폴더의 YAML 읽기·쓰기, 지표 위젯 값 계산, 템플릿, 지표 목록.
//
// 경로(쓰기는 운영 이상, webauth roleTable):
//
//	GET    /api/v1/dashboards              목록 {dashboards: [DashItem], errors: [{file, msg}], editable}      W-A crud.go
//	GET    /api/v1/dashboards/{id}         {dash, rev, editable} | 404                                       W-A crud.go
//	PUT    /api/v1/dashboards/{id}         {dash, rev} → {dash, rev} | 400 | 403(편집 꺼짐) | 409 {error, rev} W-A crud.go
//	DELETE /api/v1/dashboards/{id}?rev=    {} | 404 | 409                                                    W-A crud.go
//	GET    /api/v1/dashboards/templates    {templates: [DashTemplate]}                                       W-A templates.go
//	GET    /api/v1/dashboards/metrics      {metrics: [DashMetric]}                                           W-B metrics.go
//	GET    /api/v1/dashboards/{id}/data    ?from=&to=&g= → DashData                                          W-B data.go
//	POST   /api/v1/dashboards/data         {dash, from, to, g} → DashData | 400(미리보기, 조회 역할)         W-B data.go
package dash

import (
	"errors"
	"net/http"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/query"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// ErrNotFound는 그 ID의 보드가 없을 때다.
var ErrNotFound = errors.New("보드 없음")

// ErrConflict는 저장·삭제의 rev가 지금 파일과 다를 때(다른 사람이 먼저 바꿈), 또는 새로 만들기(rev "")인데 이미 있을 때다.
var ErrConflict = errors.New("보드가 그새 바뀜")

// Deps는 보드가 쓰는 부품이다.
type Deps struct {
	Dir      string // 보드 폴더(설정 dashboards). 비면 보드 없음(목록 빈 것, 저장 403)
	ReadOnly bool   // 설정 dashboards_readonly: 화면 저장·삭제를 끈다(파일은 사람이 고친다)
	Metrics  store.MetricStore
	State    store.StateStore // 감사 기록·서버 목록·실행 이름
	Live     *live.Cache
	Panels   *query.PanelBook // Ref 타일·kpi·템플릿·지표 목록(알림 감시자와 같은 것). nil이면 패널 없음
	Catalog  []model.MetricGroup
	Loc      *time.Location
	Now      func() time.Time
}

type handler struct {
	d    Deps
	book *Book
}

// Mount는 보드 경로를 붙인다. 카드마다 자기 mount 함수만 채운다.
func Mount(mux *http.ServeMux, d Deps) {
	if d.Panels == nil {
		d.Panels = query.PanelsOf(d.Loc)
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	h := &handler{d: d, book: NewBook(d.Dir, d.ReadOnly)}
	mountBook(mux, h)      // W-A
	mountTemplates(mux, h) // W-A
	mountData(mux, h)      // W-B
	mountMetrics(mux, h)   // W-B
}
