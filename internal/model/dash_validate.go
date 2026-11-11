package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// 보드 검증(카드 W-A). 규칙은 docs/cards/W-boards.md W-A 절.

var (
	dashIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	widgetIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	// 끝의 *는 접두사(펼친 이름마다 따로 계열).
	widgetMetricRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]+\*?$`)
)

// dashReserved는 경로 /dashboards/<이름>과 겹쳐 보드 ID로 쓸 수 없는 이름이다.
var dashReserved = []string{"templates", "metrics", "data"}

const (
	dashMinRange = 5 * time.Minute
	dashMaxRange = 168 * time.Hour
	widgetMaxW   = 12
	widgetMaxH   = 8
)

// DashIDOK는 보드 ID 규칙이다: 소문자·숫자로 시작하고 소문자·숫자·_-만 64자까지, "templates"·"metrics"·"data"는 쓸 수 없다.
func DashIDOK(id string) bool {
	return dashIDRe.MatchString(id) && !slices.Contains(dashReserved, id)
}

// WidgetIDOK는 위젯 ID 규칙이다: 소문자·숫자로 시작하고 소문자·숫자·_-만 32자까지(보드 안에서 유일).
func WidgetIDOK(id string) bool { return widgetIDRe.MatchString(id) }

// ValidateDash는 보드 정의를 검사한다. 사람이 읽을 첫 오류 하나를 돌려준다(위젯이면 "위젯 <id>: …").
func ValidateDash(d Dash) error {
	if d.ID == "" {
		return fmt.Errorf("id가 필요합니다")
	}
	if !DashIDOK(d.ID) {
		return fmt.Errorf("id %q: 보드 ID는 소문자·숫자로 시작하고 소문자·숫자·_-만 64자까지이며 templates·metrics·data는 쓸 수 없습니다", d.ID)
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("title(제목)이 필요합니다")
	}
	if n := utf8.RuneCountInString(d.Title); n > DashMaxTitle {
		return fmt.Errorf("제목은 %d자까지입니다(지금 %d자)", DashMaxTitle, n)
	}
	if d.Range != "" {
		r, err := time.ParseDuration(d.Range)
		if err != nil || r < dashMinRange || r > dashMaxRange {
			return fmt.Errorf("range %q: 5m~168h의 Go 기간(예 1h, 30m)이거나 비어야 합니다", d.Range)
		}
	}
	if len(d.Widgets) > DashMaxWidgets {
		return fmt.Errorf("위젯은 %d개까지입니다(지금 %d개)", DashMaxWidgets, len(d.Widgets))
	}
	seen := make(map[string]bool, len(d.Widgets))
	for i, w := range d.Widgets {
		if !WidgetIDOK(w.ID) {
			return fmt.Errorf("%d번째 위젯: id %q는 소문자·숫자로 시작하고 소문자·숫자·_-만 32자까지여야 합니다", i+1, w.ID)
		}
		if seen[w.ID] {
			return fmt.Errorf("위젯 %s: id가 겹칩니다(보드 안에서 유일해야 함)", w.ID)
		}
		seen[w.ID] = true
		if err := validateWidget(w); err != nil {
			return fmt.Errorf("위젯 %s: %v", w.ID, err)
		}
	}
	return nil
}

// validateWidget은 위젯 하나를 본다(오류에 위젯 ID는 넣지 않는다).
func validateWidget(w Widget) error {
	if !slices.Contains(WidgetTypes, w.Type) {
		return fmt.Errorf("모르는 종류 %q입니다(%s)", w.Type, strings.Join(WidgetTypes, "|"))
	}
	if w.W < 0 || w.W > widgetMaxW {
		return fmt.Errorf("w(폭)는 0~%d칸이어야 합니다(0은 종류 기본)", widgetMaxW)
	}
	if w.H < 0 || w.H > widgetMaxH {
		return fmt.Errorf("h(높이)는 0~%d줄이어야 합니다(0은 종류 기본)", widgetMaxH)
	}
	if n := utf8.RuneCountInString(w.Title); n > DashMaxTitle {
		return fmt.Errorf("제목은 %d자까지입니다(지금 %d자)", DashMaxTitle, n)
	}
	if IsMetricWidget(w.Type) {
		if err := validateMetricWidget(w); err != nil {
			return err
		}
	} else if len(w.Queries) > 0 || w.Ref != nil {
		return fmt.Errorf("%s 위젯은 queries·ref를 쓰지 않습니다(지표 위젯 stat·timeseries·bar·table만)", w.Type)
	}
	switch w.Reduce {
	case "", "last", "avg", "max", "min":
	default:
		return fmt.Errorf("reduce %q: last|avg|max|min이거나 비어야 합니다", w.Reduce)
	}
	if w.Top < 0 || w.Top > WidgetMaxTop {
		return fmt.Errorf("top은 0~%d이어야 합니다(0은 10)", WidgetMaxTop)
	}
	if w.Limit < 0 || w.Limit > WidgetMaxLimit {
		return fmt.Errorf("limit은 0~%d이어야 합니다(0은 종류 기본)", WidgetMaxLimit)
	}
	for _, l := range w.Levels {
		if l != "error" && l != "warn" && l != "info" {
			return fmt.Errorf("levels의 %q: error|warn|info만 쓸 수 있습니다", l)
		}
	}
	if w.WarnBelow != nil && w.CritBelow != nil && *w.CritBelow >= *w.WarnBelow {
		return fmt.Errorf("crit_below는 warn_below보다 작아야 합니다")
	}
	if w.Warn != nil && w.Crit != nil && *w.Warn > *w.Crit {
		return fmt.Errorf("warn은 crit보다 크면 안 됩니다")
	}
	if n := utf8.RuneCountInString(w.Text); n > WidgetMaxText {
		return fmt.Errorf("글은 %d자까지입니다(지금 %d자)", WidgetMaxText, n)
	}
	return nil
}

// validateMetricWidget은 지표 위젯(stat·timeseries·bar·table)의 ref·queries를 본다.
func validateMetricWidget(w Widget) error {
	nq := len(w.Queries)
	if w.Ref != nil && w.Type != WidgetStat {
		return fmt.Errorf("ref는 stat 위젯만 씁니다")
	}
	switch w.Type {
	case WidgetStat:
		if w.Ref != nil {
			if nq > 0 {
				return fmt.Errorf("stat은 ref와 queries 중 하나만 씁니다")
			}
			if w.Ref.Panel == "" || w.Ref.Tile == "" {
				return fmt.Errorf("ref에는 panel과 tile이 둘 다 필요합니다")
			}
			return nil
		}
		if nq != 1 {
			return fmt.Errorf("stat은 ref(업무 패널 타일)이거나 쿼리 정확히 1개여야 합니다(지금 %d개)", nq)
		}
	case WidgetTimeseries:
		if nq < 1 || nq > WidgetMaxQueries {
			return fmt.Errorf("timeseries는 쿼리 1~%d개여야 합니다(지금 %d개)", WidgetMaxQueries, nq)
		}
	case WidgetBar:
		if nq != 1 {
			return fmt.Errorf("bar는 쿼리 정확히 1개여야 합니다(지금 %d개)", nq)
		}
		if b := w.Queries[0].By; b != "host" && b != "owner" {
			return fmt.Errorf("bar는 by(host|owner)가 필요합니다")
		}
	case WidgetTable:
		if nq < 1 || nq > WidgetMaxQueries {
			return fmt.Errorf("table은 쿼리 1~%d개여야 합니다(지금 %d개)", WidgetMaxQueries, nq)
		}
		by := w.Queries[0].By
		if by != "host" && by != "owner" {
			return fmt.Errorf("table은 by(host|owner)가 필요합니다")
		}
		for _, q := range w.Queries[1:] {
			if q.By != by {
				return fmt.Errorf("table은 모든 쿼리의 by가 같아야 합니다(%q와 %q)", by, q.By)
			}
		}
	}
	for i, q := range w.Queries {
		if err := validateWidgetQuery(q); err != nil {
			return fmt.Errorf("%d번째 쿼리: %v", i+1, err)
		}
	}
	return nil
}

func validateWidgetQuery(q WidgetQuery) error {
	if q.Metric == "" {
		return fmt.Errorf("metric(지표 이름)이 필요합니다")
	}
	if !widgetMetricRe.MatchString(q.Metric) {
		return fmt.Errorf("metric %q: 영문·숫자·_.:-만 쓰고 접두사면 끝에만 *를 붙입니다", q.Metric)
	}
	switch q.Agg {
	case "", "sum", "max", "avg", "min":
	default:
		return fmt.Errorf("agg %q: sum|max|avg|min이거나 비어야 합니다", q.Agg)
	}
	switch q.By {
	case "", "host", "owner":
	default:
		return fmt.Errorf("by %q: host|owner이거나 비어야 합니다", q.By)
	}
	return nil
}
