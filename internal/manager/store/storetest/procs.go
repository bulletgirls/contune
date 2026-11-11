package storetest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// runProcs는 프로세스 기록(UpsertProcs·ListProcs·PruneProcs) 계약이다(06, 카드 P-C).
func runProcs(t *testing.T, open func(t *testing.T) store.StateStore) {
	ctx := context.Background()
	upsert := func(t *testing.T, s store.StateStore, ps ...model.ProcInfo) {
		t.Helper()
		if err := s.UpsertProcs(ctx, ps); err != nil {
			t.Fatal(err)
		}
	}
	keys := func(ps []model.ProcInfo) string {
		var ks []string
		for _, p := range ps {
			ks = append(ks, p.ProcKey())
		}
		return strings.Join(ks, " ")
	}
	const (
		k1 = "h1/10/1000"
		k2 = "h1/11/2000"
		k3 = "h2/10/1000"
		k4 = "h1/10/9000" // PID 재사용
	)

	t.Run("procs", func(t *testing.T) {
		s := open(t)
		upsert(t, s,
			model.ProcInfo{Host: "h1", PID: 10, StartedAt: 1000, PPID: 1, Comm: "job", ExecID: "E1", Leader: true, Owner: "E1",
				CPU: 50, RSSMB: 10, Threads: 3, Profile: true, FlowID: "f", StepID: "s", LastSeen: 1500},
			model.ProcInfo{Host: "h1", PID: 11, StartedAt: 2000, PPID: 10, Comm: "svr_a", ExecID: "E1", Service: "svr_a",
				Owner: model.ServiceOwner("svr_a", "h1"), LastSeen: 2500, EndedAt: 5000},
			model.ProcInfo{Host: "h2", PID: 10, StartedAt: 1000, Comm: "svr_a", ExecID: "E2", Service: "svr_a", LastSeen: 100},
			model.ProcInfo{Host: "h1", PID: 10, StartedAt: 9000, Comm: "redis", Service: "redis", Auto: true, LastSeen: 9500},
		)
		if err := s.UpsertProcs(ctx, nil); err != nil {
			t.Fatal(err)
		}

		// 바꾸기: exec_id·ppid·leader는 처음 값, last_seen은 뒤로 가지 않음, ended_at·exit_id는 비지 않을 때만
		upsert(t, s, model.ProcInfo{Host: "h1", PID: 10, StartedAt: 1000, PPID: 99, Comm: "job2", Exe: "/bin/job", Cmdline: "job -x",
			User: "app", ExecID: "X", Service: "svc", Auto: true, Owner: "svc:svc@h1", LastSeen: 1400})
		upsert(t, s, model.ProcInfo{Host: "h1", PID: 10, StartedAt: 1000, Comm: "job2", Exe: "/bin/job", Cmdline: "job -x", User: "app",
			Service: "svc", Auto: true, Owner: "svc:svc@h1", LastSeen: 2000, EndedAt: 3000})
		upsert(t, s, model.ProcInfo{Host: "h1", PID: 10, StartedAt: 1000, Comm: "job2", Exe: "/bin/job", Cmdline: "job -x", User: "app",
			Service: "svc", Auto: true, Owner: "svc:svc@h1", ExitID: "X1"})
		upsert(t, s, model.ProcInfo{Host: "h1", PID: 10, StartedAt: 1000, Comm: "job2", Exe: "/bin/job", Cmdline: "job -x", User: "app",
			Service: "svc", Auto: true, Owner: "svc:svc@h1"})
		got, err := s.ListProcs(ctx, model.ProcFilter{ExecID: "E1", To: 1001})
		if err != nil {
			t.Fatal(err)
		}
		want := model.ProcInfo{Host: "h1", PID: 10, StartedAt: 1000, PPID: 1, Comm: "job2", Exe: "/bin/job", Cmdline: "job -x",
			User: "app", ExecID: "E1", Service: "svc", Auto: true, Leader: true, Owner: "svc:svc@h1", LastSeen: 2000, EndedAt: 3000,
			ExitID: "X1"}
		if len(got) != 1 || fmt.Sprintf("%+v", got[0]) != fmt.Sprintf("%+v", want) {
			t.Fatalf("바꾼 뒤\n got %+v\nwant %+v", got, want)
		}

		// 지금: k1 끝(3000), k2 끝(5000), k3·k4 살아 있음
		cases := []struct {
			name string
			f    model.ProcFilter
			want string
		}{
			{"전부(시작 내림차순, 같으면 host)", model.ProcFilter{}, strings.Join([]string{k4, k2, k1, k3}, " ")},
			{"host", model.ProcFilter{Host: "h1"}, strings.Join([]string{k4, k2, k1}, " ")},
			{"exec_id", model.ProcFilter{ExecID: "E1"}, strings.Join([]string{k2, k1}, " ")},
			{"exec_ids", model.ProcFilter{ExecIDs: []string{"E1", "E2"}}, strings.Join([]string{k2, k1, k3}, " ")},
			{"exec_ids 없는 것", model.ProcFilter{ExecIDs: []string{"nope"}}, ""},
			{"service", model.ProcFilter{Service: "svr_a"}, strings.Join([]string{k2, k3}, " ")},
			{"service+host", model.ProcFilter{Service: "svr_a", Host: "h2"}, k3},
			{"alive", model.ProcFilter{Alive: true}, strings.Join([]string{k4, k3}, " ")},
			{"from: 살아 있거나 그 뒤에 끝남", model.ProcFilter{From: 4000}, strings.Join([]string{k4, k2, k3}, " ")},
			{"from 경계(ended_at = from)", model.ProcFilter{From: 3000, Host: "h1"}, strings.Join([]string{k4, k2, k1}, " ")},
			{"to: 그 전에 시작", model.ProcFilter{To: 2000}, strings.Join([]string{k1, k3}, " ")},
			{"from·to 겹침", model.ProcFilter{From: 3001, To: 9000}, strings.Join([]string{k2, k3}, " ")},
			{"limit", model.ProcFilter{Limit: 2}, strings.Join([]string{k4, k2}, " ")},
		}
		for _, c := range cases {
			got, err := s.ListProcs(ctx, c.f)
			if err != nil {
				t.Fatal(c.name, err)
			}
			if keys(got) != c.want {
				t.Errorf("%s: got %q want %q", c.name, keys(got), c.want)
			}
		}
		l, _ := s.ListProcs(ctx, model.ProcFilter{Host: "h1", Service: "redis"})
		if len(l) != 1 || !l[0].Auto || l[0].ExecID != "" || l[0].LastSeen != 9500 || l[0].CPU != 0 {
			t.Fatalf("자동 서비스 %+v", l)
		}

		// 정리: 4000 전에 끝난 k1, 4000 전에 마지막으로 본 살아 있는 k3를 지운다
		n, err := s.PruneProcs(ctx, 4000)
		if err != nil || n != 2 {
			t.Fatalf("PruneProcs %d %v", n, err)
		}
		all, _ := s.ListProcs(ctx, model.ProcFilter{})
		if keys(all) != k4+" "+k2 {
			t.Fatalf("정리 뒤 %q", keys(all))
		}
		if n, _ := s.PruneProcs(ctx, 4000); n != 0 {
			t.Fatalf("두 번째 정리 %d", n)
		}
	})

	t.Run("procs 기본 limit", func(t *testing.T) {
		s := open(t)
		ps := make([]model.ProcInfo, 510)
		for i := range ps {
			ps[i] = model.ProcInfo{Host: "h", PID: 100 + i, StartedAt: int64(1000 + i), Comm: "w", LastSeen: 1}
		}
		upsert(t, s, ps...)
		l, err := s.ListProcs(ctx, model.ProcFilter{})
		if err != nil || len(l) != 500 || l[0].PID != 609 {
			t.Fatalf("%d %v", len(l), err)
		}
	})
}
