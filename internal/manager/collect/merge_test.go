package collect

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"eum/internal/model"
)

func src(name, path string) model.LogSource {
	return model.LogSource{Name: name, Path: path, Match: []model.LogMatch{{Re: `^\(E\)`, Level: "error"}}}
}

func TestParseScope(t *testing.T) {
	for _, s := range []string{"all", "group:매매", "group:매매/체결", "group:a b/c-d", "host:match-01", "host:10.0.0.7"} {
		if err := checkScope(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"", "ALL", "all:", "group:", "group:/a", "group:a/", "group:a//b", "group:a/../b",
		"group:./a", "group:a\nb", "host:", "host:a/b", "host:a b", "host:a\tb", "grp:x", "host:" + strings.Repeat("x", 200)} {
		if err := checkScope(s); err == nil {
			t.Errorf("%q: 거절해야 함", s)
		}
	}
}

func TestScopeApplies(t *testing.T) {
	cases := []struct {
		scope, host, group string
		want               bool
	}{
		{"all", "h1", "", true},
		{"group:매매", "h1", "매매", true},
		{"group:매매", "h1", "매매/체결", true},
		{"group:매매", "h1", "매매2", false}, // 앞부분 경계
		{"group:매매", "h1", "매매2/체결", false},
		{"group:매매/체결", "h1", "매매", false},
		{"group:매매", "h1", "", false},
		{"host:h1", "h1", "매매", true},
		{"host:h1", "h10", "매매", false},
	}
	for _, c := range cases {
		if got := scopeApplies(c.scope, c.host, c.group); got != c.want {
			t.Errorf("scopeApplies(%q, %q, %q) = %v", c.scope, c.host, c.group, got)
		}
	}
}

func effString(eff []model.EffectiveSource) string {
	var s []string
	for _, e := range eff {
		s = append(s, e.Name+"@"+e.Scope+"="+e.Path)
	}
	return strings.Join(s, " ")
}

func TestMergeOrder(t *testing.T) {
	sets := map[string]model.CollectSet{}
	add := func(scope string, logs ...model.LogSource) { sets[scope] = model.CollectSet{Scope: scope, Logs: logs} }
	add("host:h1", src("step", "/h1/step.log"))
	add("group:매매/체결", src("matching", "/m/matching.log"))
	add("all", src("step", "/a/step.log"), src("common", "/a/common.log"))
	add("group:매매2", src("other", "/o/other.log"))
	add("group:매매", src("common", "/m/common.log"), src("m1", "/m/m1.log"))
	add("host:h2", src("h2only", "/h2/x.log"))

	cases := []struct{ host, group, want string }{
		// 자리는 처음 나온 곳, 덮어쓴 범위만 바뀐다. all → group:매매 → group:매매/체결 → host:h1
		{"h1", "매매/체결", "step@host:h1=/h1/step.log common@group:매매=/m/common.log m1@group:매매=/m/m1.log matching@group:매매/체결=/m/matching.log"},
		{"h2", "매매2", "step@all=/a/step.log common@all=/a/common.log other@group:매매2=/o/other.log h2only@host:h2=/h2/x.log"},
		{"h3", "매매", "step@all=/a/step.log common@group:매매=/m/common.log m1@group:매매=/m/m1.log"},
		{"h4", "", "step@all=/a/step.log common@all=/a/common.log"},
		{"h5", "매매/체결/야간", "step@all=/a/step.log common@group:매매=/m/common.log m1@group:매매=/m/m1.log matching@group:매매/체결=/m/matching.log"},
	}
	for _, c := range cases {
		if got := effString(merge(sets, c.host, c.group)); got != c.want {
			t.Errorf("%s(%s):\n got %s\nwant %s", c.host, c.group, got, c.want)
		}
	}
	if eff := merge(nil, "h1", ""); eff == nil || len(eff) != 0 {
		t.Fatalf("빈 설정은 빈 목록(nil 아님): %#v", eff)
	}
}

func TestRevContentOnly(t *testing.T) {
	sum := sha256.Sum256([]byte("[]"))
	if got := revOf(nil); got != hex.EncodeToString(sum[:])[:16] {
		t.Fatalf("빈 목록의 판 %q", got)
	}
	a := merge(map[string]model.CollectSet{"all": {Scope: "all", Logs: []model.LogSource{src("x", "/x.log")}}}, "h1", "")
	b := merge(map[string]model.CollectSet{"host:h1": {Scope: "host:h1", Logs: []model.LogSource{src("x", "/x.log")}}}, "h1", "")
	if revOf(a) != revOf(b) || len(revOf(a)) != 16 {
		t.Fatalf("같은 내용이면 범위가 달라도 같은 판: %s %s", revOf(a), revOf(b))
	}
	c := merge(map[string]model.CollectSet{"all": {Scope: "all", Logs: []model.LogSource{src("x", "/y.log")}}}, "h1", "")
	if revOf(a) == revOf(c) || revOf(a) == revOf(nil) {
		t.Fatal("내용이 바뀌면 판도 바뀐다")
	}
}

func TestHostState(t *testing.T) {
	st := func(state, rev string) *model.AgentConfigStatus {
		return &model.AgentConfigStatus{State: state, Rev: rev}
	}
	cases := []struct {
		name   string
		status *model.AgentConfigStatus
		polled string
		want   string
	}{
		{"보고 없음", nil, "", stateUnknown},
		{"보고 없이 다른 판으로 롱폴링", nil, "none", statePending},
		{"꺼짐", st(model.ConfigDisabled, ""), "", stateDisabled},
		{"실패", st(model.ConfigFailed, "W"), "W", stateFailed},
		{"판이 다름", st(model.ConfigApplied, "OLD"), "W", statePending},
		{"보고는 같지만 롱폴링 판이 다름", st(model.ConfigApplied, "W"), "OLD", statePending},
		{"일부 적용", st(model.ConfigPartial, "W"), "W", statePartial},
		{"적용됨", st(model.ConfigApplied, "W"), "W", stateApplied},
		{"적용됨(롱폴링 rev 없음)", st(model.ConfigApplied, "W"), "", stateApplied},
	}
	for _, c := range cases {
		if got := hostState("W", c.status, c.polled); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFileNames(t *testing.T) {
	none := func(string) bool { return false }
	for scope, want := range map[string]string{
		"all":           "all.yaml",
		"group:매매":      "group-매매.yaml",
		"group:매매/체결":   "group-매매-체결.yaml",
		"host:match-01": "host-match-01.yaml",
		"host:a:b*c":    "host-a_b_c.yaml",
		`group:x"y|z`:   "group-x_y_z.yaml",
	} {
		if got := pickFile(scope, none); got != want {
			t.Errorf("%s: %q, want %q", scope, got, want)
		}
	}
	taken := map[string]bool{"group-매매-체결.yaml": true}
	if got := pickFile("group:매매-체결", func(n string) bool { return taken[n] }); got != "group-매매-체결-2.yaml" {
		t.Fatalf("충돌: %q", got)
	}
	taken["group-매매-체결-2.yaml"] = true
	if got := pickFile("group:매매-체결", func(n string) bool { return taken[n] }); got != "group-매매-체결-3.yaml" {
		t.Fatalf("두 번 충돌: %q", got)
	}
}
