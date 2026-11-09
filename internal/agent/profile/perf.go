package profile

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// perfConfig는 perf 프로파일러 설정이다(profiler: {type: perf, hz, call_graph, perf, tmp_dir}).
type perfConfig struct {
	Hz        int    // 초당 표본 수, 기본 49
	CallGraph string // dwarf | fp | none, 기본 dwarf
	Perf      string // perf 실행 파일, 기본 "perf"
	TmpDir    string // 임시 파일 폴더, "" = 시스템 기본
}

// parsePerfConfig는 설정 맵을 해석하고 빈 값에 기본값을 채운다.
func parsePerfConfig(cfg map[string]any) (perfConfig, error) {
	c := perfConfig{Hz: 49, CallGraph: "dwarf", Perf: "perf"}
	if v, ok := cfg["hz"]; ok {
		switch x := v.(type) {
		case int:
			c.Hz = x
		case int64:
			c.Hz = int(x)
		case float64:
			c.Hz = int(x)
		default:
			return c, fmt.Errorf("perf: hz %v는 숫자가 아님", v)
		}
		if c.Hz <= 0 {
			return c, fmt.Errorf("perf: hz %d는 1 이상이어야 함", c.Hz)
		}
	}
	if s, _ := cfg["call_graph"].(string); s != "" {
		switch s {
		case "dwarf", "fp", "none":
			c.CallGraph = s
		default:
			return c, fmt.Errorf("perf: call_graph %q는 dwarf|fp|none 중 하나여야 함", s)
		}
	}
	if s, _ := cfg["perf"].(string); s != "" {
		c.Perf = s
	}
	if s, _ := cfg["tmp_dir"].(string); s != "" {
		c.TmpDir = s
	}
	return c, nil
}

// recordArgs는 pids에 옆에서 붙어 window 동안 표본을 out에 쓰는 perf record 인자를 만든다.
// 배치를 감싸지 않으므로 명령은 언제나 `sleep <초>`다.
//
// 예: recordArgs(perfConfig{Hz: 49, CallGraph: "fp"}, []int{1, 2}, 10*time.Second, "/tmp/a.data")
// → record -q -e cpu-clock -F 49 -g -p 1,2 -o /tmp/a.data -- sleep 10
func recordArgs(cfg perfConfig, pids []int, window time.Duration, out string) []string {
	args := []string{"record", "-q", "-e", "cpu-clock", "-F", strconv.Itoa(cfg.Hz)}
	switch cfg.CallGraph {
	case "dwarf":
		args = append(args, "--call-graph", "dwarf")
	case "fp":
		args = append(args, "-g")
	}
	ps := make([]string, len(pids))
	for i, p := range pids {
		ps[i] = strconv.Itoa(p)
	}
	return append(args, "-p", strings.Join(ps, ","), "-o", out,
		"--", "sleep", strconv.FormatFloat(window.Seconds(), 'f', -1, 64))
}

// head는 b의 앞 n바이트를 공백을 다듬어 돌려준다(에러 메시지용 stderr 요약).
func head(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return strings.TrimSpace(string(b))
}

// withStderr는 perf 실패 에러에 stderr 앞 500바이트를 붙인다(비었으면 붙이지 않음).
func withStderr(what string, err error, stderr []byte) error {
	if msg := head(stderr, 500); msg != "" {
		return fmt.Errorf("%s: %w: %s", what, err, msg)
	}
	return fmt.Errorf("%s: %w", what, err)
}
