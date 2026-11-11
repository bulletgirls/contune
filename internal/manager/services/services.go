// Package services는 서비스다(06, 카드 P-D): services/*.yaml 정의, 프로세스 기록에서 보이는 자동 서비스,
// 서버별 인스턴스 상태, 서비스 시간축의 사건, 에이전트에 내려 줄 정의, 서비스 미기동·선언(starts) 확인 알림.
//
// 계약 stub: 레인 D가 채운다. 다른 레인은 아래 시그니처만 믿는다(nil *Book도 안전: 빈 값).
package services

import (
	"context"
	"net/http"
	"time"

	"eum/internal/manager/alert"
	"eum/internal/manager/live"
	"eum/internal/manager/procs"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 서비스가 쓰는 부품이다.
type Deps struct {
	Dir    string // services/*.yaml 폴더. 비면 정의 없음(자동 서비스만)
	State  store.StateStore
	Live   *live.Cache
	Procs  *procs.Registry
	Flows  func() []model.Flow
	Panels func() []model.Panel // service: 로 서비스를 가리키는 패널(목록의 Panel 칸)
	Hosts  func() []model.Host  // 그룹(정의의 Group과 맞추기)
	Loc    *time.Location       // Expect.Active를 보는 tz
	Now    func() time.Time
}

// Book은 서비스 정의와 상태다.
type Book struct {
	d Deps
}

// New는 Dir을 읽은 Book이다. 폴더를 못 읽거나 틀린 파일은 로그만 남기고 건너뛴다.
func New(d Deps) *Book { return &Book{d: d} }

// Defs는 읽은 정의(검사 통과, ID 오름차순)다.
func (b *Book) Defs() []model.ServiceDef { return nil }

// ForAgent는 그 호스트(그룹)에 내려 줄 정의와 그 판(정의가 바뀌면 바뀌는 짧은 문자열)이다. 수집 설정(collect)이 AgentConfig에 싣는다.
func (b *Book) ForAgent(host, group string) ([]model.ServiceDef, string) { return nil, "" }

// List는 정의 + 자동 서비스의 목록과 인스턴스다(GET /api/v1/services).
func (b *Book) List(ctx context.Context) []model.ServiceItem { return nil }

// AlertConds는 알림 감시자가 주기마다 부르는 서비스 조건이다(서비스 미기동, 선언한 서비스가 뜨지 않음).
func (b *Book) AlertConds(ctx context.Context, now int64) []alert.ServiceCond { return nil }

// Mount는 GET /api/v1/services, GET /api/v1/services/{id}?from=&to=, POST /api/v1/services/reload(관리)를 붙인다.
func (b *Book) Mount(mux *http.ServeMux) {}
