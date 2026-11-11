package remote

import (
	"os"
	"path/filepath"
	"testing"

	"eum/internal/agent/logtail"
	"eum/internal/api"
	"eum/internal/model"
)

// TestOnServicesAndLogService: 캐시의 설정을 적용할 때 서비스 정의를 콜백으로 넘기고, 출처의 service 문법을 검사한다.
func TestOnServicesAndLogService(t *testing.T) {
	base := t.TempDir()
	logDir, workDir := filepath.Join(base, "logs"), filepath.Join(base, "work")
	for _, d := range []string{logDir, workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCache(t, workDir, model.AgentConfig{
		Rev:      "R1",
		Services: []model.ServiceDef{{ID: "matching", Title: "매칭", Match: []model.ServiceMatch{{Comm: "matching"}}}},
		Logs: []model.LogSource{
			{Name: "ok", Path: "{log_dir}/ok.log", Match: errRule, Service: "matching"},
			{Name: "bad", Path: "{log_dir}/bad.log", Match: errRule, Service: "Bad ID"},
		},
	})
	tl, err := logtail.New("h1", logDir, nil, logtail.Options{ReadRoots: []string{logDir}})
	if err != nil {
		t.Fatal(err)
	}
	var got []model.ServiceDef
	calls := 0
	r := New(api.NewClient("http://127.0.0.1:1", "tok"), tl, Options{
		Host: "h1", Group: "매매", LogDir: logDir, WorkDir: workDir, Enabled: true, ReadRoots: []string{logDir},
		OnServices: func(d []model.ServiceDef) { got, calls = d, calls+1 },
	})
	if calls != 1 || len(got) != 1 || got[0].ID != "matching" {
		t.Fatalf("OnServices 호출 %d번, 값 %+v", calls, got)
	}
	s := r.status()
	if s.State != model.ConfigPartial {
		t.Errorf("틀린 service 출처는 건너뛰어 partial이어야 함: %s", s.State)
	}
	if len(s.Sources) != 2 || s.Sources[0].Skipped != "" || s.Sources[1].Why != model.SkipInvalid {
		t.Errorf("sources = %+v", s.Sources)
	}

	// 꺼져 있으면 부르지 않는다.
	calls = 0
	New(api.NewClient("http://127.0.0.1:1", "tok"), tl, Options{
		Host: "h1", LogDir: logDir, WorkDir: workDir, Enabled: false,
		OnServices: func([]model.ServiceDef) { calls++ },
	})
	if calls != 0 {
		t.Errorf("꺼진 원격이 OnServices를 %d번 부름", calls)
	}
}
