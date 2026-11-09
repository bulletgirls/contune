package profile

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

var (
	headRe = regexp.MustCompile(`\s+\d+(?:/\d+)?\s`)
	dsoRe  = regexp.MustCompile(`\s+\([^()]*\)\s*$`)
	offRe  = regexp.MustCompile(`\+0x[0-9a-fA-F]+$`)
	verRe  = regexp.MustCompile(`@@?[A-Za-z0-9_.]+$`)
	// 바깥쪽 시작 프레임: 함수 이름이 아니라 런타임 진입 코드다.
	startFrames = map[string]bool{
		"_start": true, "__libc_start_main": true, "__libc_start_call_main": true,
		"start_thread": true, "clone": true, "clone3": true,
	}
)

// ParsePerfScript는 `perf script` 기본 출력을 접힌 스택("comm;바깥;…;안쪽") → 표본 수로 바꾼다.
// 심볼의 +0x 오프셋과 @@버전 접미사, 시작 프레임을 지우고 이어진 [unknown]은 하나로 합친다.
//
// 예:
//
//	m, err := ParsePerfScript(strings.NewReader(out))
//	// m["matching_svc;main;handle_order;match_order;book_insert"] == 2
func ParsePerfScript(r io.Reader) (map[string]int64, error) {
	out := map[string]int64{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var comm string
	var frames []string // 안쪽이 먼저
	in := false
	flush := func() {
		if !in {
			return
		}
		parts := []string{comm}
		for i := len(frames) - 1; i >= 0; i-- {
			parts = append(parts, frames[i])
		}
		out[strings.Join(parts, ";")]++
		in, frames = false, nil
	}
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			flush()
			loc := headRe.FindStringIndex(line)
			if loc == nil {
				comm = strings.TrimSpace(line)
			} else {
				comm = line[:loc[0]]
			}
			in = true
			continue
		}
		if !in {
			continue
		}
		sym := cleanSymbol(line)
		if sym == "" || startFrames[sym] {
			continue
		}
		if sym == "[unknown]" && len(frames) > 0 && frames[len(frames)-1] == "[unknown]" {
			continue
		}
		frames = append(frames, sym)
	}
	flush()
	return out, sc.Err()
}

// cleanSymbol은 "주소 심볼 (dso)" 프레임 줄에서 정리된 심볼을 돌려준다.
func cleanSymbol(line string) string {
	s := strings.TrimSpace(line)
	s = dsoRe.ReplaceAllString(s, "")
	_, sym, ok := strings.Cut(s, " ")
	if !ok {
		return "[unknown]"
	}
	sym = strings.TrimSpace(sym)
	sym = offRe.ReplaceAllString(sym, "")
	sym = verRe.ReplaceAllString(sym, "")
	if sym == "" {
		return "[unknown]"
	}
	return sym
}
