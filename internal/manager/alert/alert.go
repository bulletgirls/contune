// Package alert는 알림 감시자다: 주기마다 상태를 계산하고 바뀐 순간에만 알림을 만든다(05 §5).
package alert

import (
	"context"
	"net/http"
	"time"

	"eum/internal/manager/live"
	"eum/internal/manager/notify"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Config는 매니저 설정 alerts(+ notify.min_level)다.
type Config struct {
	Every         time.Duration `yaml:"every" json:"every"`
	IncidentAfter time.Duration `yaml:"incident_after" json:"incident_after"`
	IncidentClear time.Duration `yaml:"incident_clear" json:"incident_clear"`
	LogLevels     []string      `yaml:"log_levels" json:"log_levels"`
	AutoIncident  bool          `yaml:"auto_incident" json:"auto_incident"`
	MinNotify     string        `yaml:"-" json:"min_notify"` // notify.min_level에서 채움
}

// Deps는 감시자가 쓰는 부품이다.
type Deps struct {
	Metrics    store.MetricStore
	State      store.StateStore
	Live       *live.Cache
	Flows      func() []model.Flow
	Panels     []model.Panel
	Thresholds model.HostThresholds
	Notifiers  []notify.Notifier
	Risk       func() (float64, bool)
	RiskWarn   float64
	RiskCrit   float64
	Cfg        Config
	Now        func() time.Time
}

// Watcher는 알림 감시자다. (stub: X1이 채움)
type Watcher struct {
	d Deps
}

// New는 감시자를 만든다.
func New(d Deps) *Watcher { return &Watcher{d: d} }

// Run은 Cfg.Every마다 상태를 계산한다. (stub: 대기)
func (w *Watcher) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Mount는 GET /api/v1/alerts, POST /api/v1/alerts/{id}/ack를 붙인다. (stub: 등록 없음)
func (w *Watcher) Mount(mux *http.ServeMux) {}
