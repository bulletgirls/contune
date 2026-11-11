package model

// 사용자 보드(07, 카드 W). 매니저 설정 dashboards 폴더의 <id>.yaml 하나 = 보드 하나이고 파일이 기준이다.
//
// 보드는 보기만 한다: 위젯의 기준(warn·crit·warn_below·crit_below)은 색과 기준선만 바꾸고 알림을 내지 않는다.
// 알림은 업무 패널 타일(05 §18)의 몫이고, 위젯이 그 타일을 그대로(같은 판정·같은 링크) 보이려면 Ref로 가리킨다.
// 관제 화면(#dashboard)은 고정이고, 보드는 그 옆에 둔다(템플릿 "ops"로 관제와 같은 구성에서 시작할 수 있다).

// 위젯 종류.
const (
	// 지표 위젯: 매니저가 GET /dashboards/{id}/data(미리보기는 POST /dashboards/data)에서 값을 계산한다.
	WidgetStat       = "stat"       // 값 하나 + 추이선(+ 기준이 있으면 불릿). Ref면 업무 패널 타일 그대로
	WidgetTimeseries = "timeseries" // 시계열 선: 쿼리마다(By로 나누면 나눈 키마다) 선 하나
	WidgetBar        = "bar"        // 가로 막대: By로 나눈 키마다 값 하나(Reduce), 큰 순 Top개
	WidgetTable      = "table"      // 표: 줄 = By로 나눈 키, 칸 = 쿼리마다 값 하나(Reduce), 첫 칸 큰 순 Top개
	WidgetText       = "text"       // 글(메모·담당·연락처). 화면이 그린다
	// 도메인 위젯: 화면이 기존 API(/hosts·/alerts·/flows·/flows/hours·/panels·/risk·/services·/logs)로 그린다.
	WidgetKPI       = "kpi"       // 업무 지표 KPI 타일(관제의 업무 지표 카드). Scope.Panel이 있으면 그 패널의 타일 전부
	WidgetEvents    = "events"    // 실시간 이벤트(firing 알림, 확인 표시)
	WidgetServers   = "servers"   // 서버 상태 고리 + 이상 서버
	WidgetServerMap = "servermap" // 서버 맵(그룹 줄, 칸 = 서버)
	WidgetBatch     = "batch"     // 금일 배치 고리 + 주목할 배치
	WidgetHours     = "hours"     // 시간대별 배치
	WidgetRisk      = "risk"      // 장애 위험도
	WidgetLogs      = "logs"      // 업무 로그 목록
	WidgetServices  = "services"  // 서비스 인스턴스 상태 목록
)

// WidgetTypes는 알려진 위젯 종류다(편집기의 고르기 순서).
var WidgetTypes = []string{
	WidgetStat, WidgetTimeseries, WidgetBar, WidgetTable, WidgetText,
	WidgetKPI, WidgetEvents, WidgetServers, WidgetServerMap, WidgetBatch, WidgetHours, WidgetRisk, WidgetLogs, WidgetServices,
}

// IsMetricWidget은 매니저가 /data에서 값을 계산하는 위젯 종류인가(stat·timeseries·bar·table).
func IsMetricWidget(t string) bool {
	switch t {
	case WidgetStat, WidgetTimeseries, WidgetBar, WidgetTable:
		return true
	}
	return false
}

// widgetSize는 종류별 기본 크기(폭 칸, 높이 줄)다. 화면 등록부(widgets/index.js)와 같다.
var widgetSize = map[string][2]int{
	WidgetStat: {3, 1}, WidgetTimeseries: {6, 2}, WidgetBar: {4, 2}, WidgetTable: {6, 2}, WidgetText: {3, 1},
	WidgetKPI: {3, 2}, WidgetEvents: {4, 4}, WidgetServers: {3, 2}, WidgetServerMap: {8, 3}, WidgetBatch: {3, 2},
	WidgetHours: {8, 2}, WidgetRisk: {3, 2}, WidgetLogs: {6, 2}, WidgetServices: {4, 2},
}

// WidgetDefaultSize는 종류의 기본 폭(1~12칸)과 높이(1~8줄)다. 모르는 종류는 (4, 2).
func WidgetDefaultSize(t string) (w, h int) {
	if s, ok := widgetSize[t]; ok {
		return s[0], s[1]
	}
	return 4, 2
}

// 보드 한도(ValidateDash가 본다).
const (
	DashMaxWidgets   = 60
	WidgetMaxQueries = 8
	WidgetMaxTop     = 50
	WidgetMaxLimit   = 200
	WidgetMaxText    = 2000 // 글자(rune)
	DashMaxTitle     = 80   // 보드·위젯 제목 글자(rune)
)

// Dash는 사용자 보드다.
type Dash struct {
	ID    string `json:"id" yaml:"id"` // 파일 이름(확장자 뺀 것)과 같다(DashIDOK)
	Title string `json:"title" yaml:"title"`
	Desc  string `json:"desc,omitempty" yaml:"desc,omitempty"`
	// Range는 처음 열 때 보는 구간(Go 기간 5m~168h). 비면 1h. 끝은 늘 지금이다.
	Range string `json:"range,omitempty" yaml:"range,omitempty"`
	// Group은 범위가 빈 위젯의 서버 그룹(접두사: "매매"는 "매매/체결"을 포함). 위젯 Scope.Group > 보드 Group > 트리에서 고른 그룹.
	Group   string   `json:"group,omitempty" yaml:"group,omitempty"`
	Widgets []Widget `json:"widgets" yaml:"widgets"`
	// 매니저가 저장할 때 채운다(화면이 보낸 값은 무시한다).
	Owner     string `json:"owner,omitempty" yaml:"owner,omitempty"`           // 처음 만든 사람(감사 기록의 actor, 예 user:operator)
	UpdatedBy string `json:"updated_by,omitempty" yaml:"updated_by,omitempty"` // 마지막으로 저장한 사람
	UpdatedAt int64  `json:"updated_at,omitempty" yaml:"updated_at,omitempty"` // 마지막 저장 시각(ms)
}

// Widget은 보드의 칸 하나다. 배치는 Widgets 순서대로 12칸 격자에 왼쪽 위부터 채운다(빈자리 메우기 없음).
type Widget struct {
	ID    string `json:"id" yaml:"id"` // 보드 안에서 유일(WidgetIDOK)
	Type  string `json:"type" yaml:"type"`
	Title string `json:"title,omitempty" yaml:"title,omitempty"` // 비면 종류 이름
	W     int    `json:"w,omitempty" yaml:"w,omitempty"`         // 폭 1~12칸(12 = 한 줄). 0이면 종류 기본
	H     int    `json:"h,omitempty" yaml:"h,omitempty"`         // 높이 1~8줄(한 줄 = 화면 약 110px). 0이면 종류 기본

	Scope WidgetScope `json:"scope" yaml:"scope,omitempty"`

	// 지표 위젯(stat·timeseries·bar·table)
	Queries   []WidgetQuery `json:"queries,omitempty" yaml:"queries,omitempty"`
	Unit      string        `json:"unit,omitempty" yaml:"unit,omitempty"`     // 비면 첫 쿼리 지표의 단위(카탈로그·패널 타일)
	Reduce    string        `json:"reduce,omitempty" yaml:"reduce,omitempty"` // stat·bar·table의 값 하나: last(기본, 구간의 마지막 값) | avg | max | min
	Warn      *float64      `json:"warn,omitempty" yaml:"warn,omitempty"`     // 이상이면 경고(표시만)
	Crit      *float64      `json:"crit,omitempty" yaml:"crit,omitempty"`
	WarnBelow *float64      `json:"warn_below,omitempty" yaml:"warn_below,omitempty"` // 이하이면 경고(표시만)
	CritBelow *float64      `json:"crit_below,omitempty" yaml:"crit_below,omitempty"`
	Max       *float64      `json:"max,omitempty" yaml:"max,omitempty"`     // timeseries y축 위 끝(% 사용률은 100). 비면 데이터에 맞춤
	Stack     bool          `json:"stack,omitempty" yaml:"stack,omitempty"` // timeseries 쌓은 영역
	Top       int           `json:"top,omitempty" yaml:"top,omitempty"`     // bar·table 줄 수(0이면 10, 최대 50)
	Ref       *TileRef      `json:"ref,omitempty" yaml:"ref,omitempty"`     // stat만: 업무 패널 타일 그대로(Queries 없이)

	// 도메인 위젯
	Levels []string `json:"levels,omitempty" yaml:"levels,omitempty"` // logs: error|warn|info 중(비면 error, warn)
	Limit  int      `json:"limit,omitempty" yaml:"limit,omitempty"`   // events·logs·services 줄 수(0이면 종류 기본, 최대 200)
	Text   string   `json:"text,omitempty" yaml:"text,omitempty"`     // text: 글(줄바꿈 유지, HTML 아님)
}

// WidgetQuery는 지표 위젯의 쿼리 하나다: 지표 · 같은 시각 계열 합치기 · 나눠 보기. 범위는 위젯 Scope(> 보드 Group > 트리).
type WidgetQuery struct {
	Metric string `json:"metric" yaml:"metric"`                   // 지표 이름. 끝이 *면 접두사(펼친 이름마다 따로 계열)
	Label  string `json:"label,omitempty" yaml:"label,omitempty"` // 계열 이름. 비면 카탈로그·패널 제목, 없으면 지표 이름
	Agg    string `json:"agg,omitempty" yaml:"agg,omitempty"`     // 같은 시각의 계열 합치기: sum | max | avg | min. 비면 avg
	By     string `json:"by,omitempty" yaml:"by,omitempty"`       // 나눠 보기: ""(하나로) | host(서버마다) | owner(주인마다: 실행·서비스 인스턴스)
}

// WidgetScope는 위젯이 보는 범위다. 비는 칸은 보지 않는다(Group은 보드 Group·트리 그룹을 물려받는다).
type WidgetScope struct {
	Group   string   `json:"group,omitempty" yaml:"group,omitempty"`     // 서버 그룹(접두사)
	Hosts   []string `json:"hosts,omitempty" yaml:"hosts,omitempty"`     // 서버(있으면 Group보다 우선)
	Service string   `json:"service,omitempty" yaml:"service,omitempty"` // 서비스 ID: 지표·로그는 주인 키 svc:<서비스>@*만, host.*·agent.*는 그 서비스가 도는 서버만
	Board   string   `json:"board,omitempty" yaml:"board,omitempty"`     // 배치판(플로우 board 접두사): batch·hours
	Panel   string   `json:"panel,omitempty" yaml:"panel,omitempty"`     // kpi: 이 업무 패널의 타일 전부(비면 범위 안 패널의 kpi: true 타일)
}

// TileRef는 업무 패널 타일 하나다(패널 ID + 타일 제목).
type TileRef struct {
	Panel string `json:"panel" yaml:"panel"`
	Tile  string `json:"tile" yaml:"tile"`
}

// DashItem은 보드 목록의 한 줄이다(GET /dashboards).
type DashItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Desc      string `json:"desc,omitempty"`
	Widgets   int    `json:"widgets"`
	Owner     string `json:"owner,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
	Rev       string `json:"rev"` // 파일 내용의 sha256 앞 16자(저장·삭제의 낙관적 잠금)
}

// DashData는 보드 지표 위젯의 값이다(GET /dashboards/{id}/data, POST /dashboards/data).
type DashData struct {
	From    int64                 `json:"from"`
	To      int64                 `json:"to"`
	Step    int                   `json:"step"` // 초. /series와 같은 autoStep(점 600개 안쪽)
	Now     int64                 `json:"now"`
	Widgets map[string]WidgetData `json:"widgets"` // 지표 위젯 ID → 값. 도메인·글 위젯은 없다
}

// WidgetData는 지표 위젯 하나의 값이다.
type WidgetData struct {
	Error  string         `json:"error,omitempty"`  // 그리지 못한 이유(화면은 위젯 안에 그대로 보인다). 예: "패널 matching에 타일 '대기 큐' 없음"
	Unit   string         `json:"unit,omitempty"`   // 위젯 Unit, 비면 첫 쿼리 지표의 단위
	Series []WidgetSeries `json:"series,omitempty"` // timeseries 선, stat 추이선(하나)
	Value  *float64       `json:"value,omitempty"`  // stat 값(Reduce). 값이 없으면 null(칸 없음)
	State  string         `json:"state,omitempty"`  // stat: none | ok | warn | crit (위젯 기준, 기준이 없으면 ok. Ref면 패널 판정)
	Rows   []WidgetRow    `json:"rows,omitempty"`   // bar·table
	Cols   []WidgetCol    `json:"cols,omitempty"`   // table 칸(쿼리마다). bar는 하나
	Tile   any            `json:"tile,omitempty"`   // stat Ref: GET /panels의 타일과 같은 모양(title·unit·warn…·value·state·reason·off·host·exec_id·service·ts)
}

// WidgetSeries는 선 하나다.
type WidgetSeries struct {
	Query  int          `json:"query"`         // Queries 번호(Ref면 0)
	Metric string       `json:"metric"`        // 실제 지표 이름(접두사면 펼친 이름)
	Key    string       `json:"key,omitempty"` // By로 나눈 키: 서버 ID 또는 주인 키(""면 하나로 합침)
	Label  string       `json:"label"`         // 범례 이름
	Unit   string       `json:"unit,omitempty"`
	Points [][2]float64 `json:"points"` // [ts(ms), value], 시각 순
}

// WidgetRow는 bar·table의 줄 하나다(By로 나눈 키 하나).
type WidgetRow struct {
	Key    string     `json:"key"`    // 서버 ID 또는 주인 키
	Label  string     `json:"label"`  // 서버 ID · "서비스@서버" · "플로우 › 스텝"(실행)
	Values []*float64 `json:"values"` // 칸(쿼리)마다 값 하나(Reduce), 없으면 null
	States []string   `json:"states"` // 칸마다 none | ok | warn | crit (위젯 기준을 모든 칸에)
}

// WidgetCol은 table 칸 하나다.
type WidgetCol struct {
	Label  string `json:"label"`
	Metric string `json:"metric"`
	Unit   string `json:"unit,omitempty"`
}

// DashTemplate은 새 보드의 시작점이다(GET /dashboards/templates).
type DashTemplate struct {
	ID    string `json:"id"` // "blank" | "ops"(관제와 같은 구성) | "panel:<패널 ID>"(업무 패널의 타일·차트·로그)
	Title string `json:"title"`
	Desc  string `json:"desc,omitempty"`
	Dash  Dash   `json:"dash"` // ID는 비어 있다(저장할 때 정한다)
}

// DashMetric은 편집기 지표 고르기의 한 줄이다(GET /dashboards/metrics).
type DashMetric struct {
	Name     string   `json:"name"`
	Title    string   `json:"title,omitempty"`    // 카탈로그 "차트 · 항목" 또는 패널 타일 제목
	Unit     string   `json:"unit,omitempty"`     // 카탈로그 → 패널 타일 → 비움
	Desc     string   `json:"desc,omitempty"`     // 카탈로그 설명
	Source   string   `json:"source"`             // catalog | panel | live(처음 찾은 곳)
	Live     bool     `json:"live"`               // 지난 10분 안에 값이 들어옴
	Hosts    int      `json:"hosts"`              // 값을 보낸 서버 수(live)
	Services []string `json:"services,omitempty"` // 값을 낸 서비스(live 주인 키에서, 이름 순)
}
