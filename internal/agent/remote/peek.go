package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"eum/internal/agent/logtail"
	"eum/internal/model"
)

const (
	peekMaxBytes     = 256 * 1024 // 끝에서 읽는 크기
	peekLineBytes    = 4096       // 줄마다 자르는 길이(logtail과 같음)
	peekDefaultLines = 200
	peekMaxLines     = 500
	rateGap          = time.Second // rate를 재는 간격
)

// peek은 출처 경로 문법({log_dir}·{exec_id}·*)에 맞는 파일 중 가장 최근 것의 끝 줄을 읽는다.
// 경로의 폴더 부분이 read_roots 밖이면 찾아보지도 않고, 고른 파일의 실제 경로가 read_roots 밖이면 거절한다.
func (r *Remote) peek(ctx context.Context, q model.CollectRequest) model.CollectResult {
	var res model.CollectResult
	if msg := model.CheckLogPath(q.Path); msg != "" {
		res.Error = msg
		return res
	}
	n := q.Lines
	if n <= 0 {
		n = peekDefaultLines
	}
	n = min(n, peekMaxLines)
	if !logtail.InRoots(r.o.ReadRoots, r.dirOf(q.Path)) {
		res.Error = logtail.ErrOutsideRoots.Error()
		return res
	}
	paths, err := logtail.Glob(q.Path, r.o.LogDir)
	if err != nil {
		res.Error = "경로를 해석하지 못함: " + err.Error()
		return res
	}
	var best string
	var bestMod time.Time
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if best == "" || fi.ModTime().After(bestMod) {
			best, bestMod = p, fi.ModTime()
		}
	}
	if best == "" {
		res.Error = "경로에 맞는 파일이 없습니다"
		return res
	}
	f, err := logtail.OpenIn(r.o.ReadRoots, best)
	if err != nil {
		if errors.Is(err, logtail.ErrOutsideRoots) {
			res.Error = err.Error()
		} else {
			res.Error = "파일을 열지 못함: " + err.Error()
		}
		return res
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		res.Error = "파일 상태를 읽지 못함: " + err.Error()
		return res
	}
	size := fi.Size()
	start := max(0, size-peekMaxBytes)
	buf := make([]byte, size-start)
	got, err := f.ReadAt(buf, start)
	if err != nil && err != io.EOF {
		res.Error = "파일을 읽지 못함: " + err.Error()
		return res
	}
	buf = buf[:got]
	if start > 0 { // 맨 앞 줄은 잘렸을 수 있다
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}
	raw := bytes.Split(buf, []byte{'\n'})
	if len(raw) > 0 && len(raw[len(raw)-1]) == 0 {
		raw = raw[:len(raw)-1]
	}
	lines := make([]string, 0, min(n, len(raw)))
	for _, l := range raw[max(0, len(raw)-n):] {
		lines = append(lines, strings.ToValidUTF8(logtail.Clip(l, peekLineBytes), "�"))
	}
	res.File, res.Size, res.Lines = best, size, lines

	// 초당 줄 수 추정: 1초 동안 늘어난 바이트 ÷ 읽은 줄의 평균 길이(줄바꿈 포함). 줄었으면 0.
	if len(raw) > 0 && r.wait(ctx, rateGap) {
		if fi2, err := f.Stat(); err == nil && fi2.Size() > size {
			avg := float64(len(buf)) / float64(len(raw))
			res.Rate = float64(fi2.Size()-size) / avg / rateGap.Seconds()
		}
	}
	return res
}
