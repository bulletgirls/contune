package risk

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"eum/internal/manager/store"
	"eum/internal/model"
)

// ImportIncidents는 장애 대장 CSV(start,end,scope,title)를 넣고 넣은 수를 돌려준다(05 §6.1).
// 머리 줄을 허용하고, 시각은 RFC3339 또는 "YYYY-MM-DD HH:MM"(loc)이다. end가 비면 진행 중.
// 잘못된 줄은 줄 번호를 담은 에러로 모아 돌려주되 맞는 줄은 넣는다.
func ImportIncidents(ctx context.Context, st store.StateStore, r io.Reader, loc *time.Location) (int, error) {
	if loc == nil {
		loc = time.Local
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var errs []error
	n, line := 0, 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if text == "" {
			continue
		}
		rec, err := csv.NewReader(strings.NewReader(text)).Read()
		if err != nil {
			errs = append(errs, fmt.Errorf("%d번째 줄: CSV를 읽지 못함: %v", line, err))
			continue
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		if n == 0 && len(errs) == 0 && strings.EqualFold(rec[0], "start") {
			continue // 머리 줄
		}
		in, err := parseIncident(rec, loc)
		if err != nil {
			errs = append(errs, fmt.Errorf("%d번째 줄: %v", line, err))
			continue
		}
		if _, err := st.CreateIncident(ctx, in); err != nil {
			errs = append(errs, fmt.Errorf("%d번째 줄: 저장 실패: %v", line, err))
			continue
		}
		n++
	}
	if err := sc.Err(); err != nil {
		errs = append(errs, fmt.Errorf("%d번째 줄 뒤: %v", line, err))
	}
	return n, errors.Join(errs...)
}

func parseIncident(rec []string, loc *time.Location) (model.Incident, error) {
	for len(rec) < 4 {
		rec = append(rec, "")
	}
	start, err := parseTime(rec[0], loc)
	if err != nil {
		return model.Incident{}, fmt.Errorf("start %v", err)
	}
	var end int64
	if rec[1] != "" {
		if end, err = parseTime(rec[1], loc); err != nil {
			return model.Incident{}, fmt.Errorf("end %v", err)
		}
	}
	in := model.Incident{Start: start, End: end, Scope: rec[2], Title: rec[3], Source: "import"}
	if in.Title == "" {
		in.Title = "장애"
	}
	if msg := checkIncident(in); msg != "" {
		return model.Incident{}, errors.New(msg)
	}
	return in, nil
}

func parseTime(s string, loc *time.Location) (int64, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, loc); err == nil {
		return t.UnixMilli(), nil
	}
	return 0, fmt.Errorf("%q는 RFC3339나 YYYY-MM-DD HH:MM이 아님", s)
}
