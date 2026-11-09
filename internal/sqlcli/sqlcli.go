// Package sqlcli는 eum sql이다: 두 DB를 읽기 전용으로 열어 SQL 조회를 한다(02 §6.1).
package sqlcli

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	_ "modernc.org/sqlite"
)

var errReadOnly = errors.New("읽기 전용입니다: SELECT 질의만 실행할 수 있습니다")

// Run은 dataDir의 metrics.db를 읽기 전용으로 열고 state.db를 붙여 query를 실행해 w에 찍는다.
func Run(dataDir, query string, csvOut bool, w io.Writer) error {
	if !isReadQuery(query) {
		return errReadOnly
	}
	metrics, err := filepath.Abs(filepath.Join(dataDir, "metrics.db"))
	if err != nil {
		return err
	}
	state, err := filepath.Abs(filepath.Join(dataDir, "state.db"))
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", fileURI(metrics))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // PRAGMA·ATTACH가 질의와 같은 연결에 남도록
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return fmt.Errorf("metrics.db 열기: %w", err)
	}
	if _, err := db.ExecContext(ctx, "ATTACH ? AS st", fileURI(state)); err != nil {
		return fmt.Errorf("state.db 붙이기: %w", err)
	}
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return wrapErr(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	var data [][]cell
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]cell, len(cols))
		for i, v := range vals {
			row[i] = toCell(v)
		}
		data = append(data, row)
	}
	if err := rows.Err(); err != nil {
		return wrapErr(err)
	}
	if csvOut {
		return writeCSV(w, cols, data)
	}
	return writeTable(w, cols, data)
}

func fileURI(path string) string {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	return u.String()
}

// isReadQuery는 SELECT·WITH·VALUES로 시작하는 질의만 통과시킨다(ATTACH·PRAGMA 등을 막는 1차 관문).
func isReadQuery(q string) bool {
	f := strings.Fields(strings.TrimLeft(q, " \t\r\n;("))
	if len(f) == 0 {
		return false
	}
	switch strings.ToUpper(f[0]) {
	case "SELECT", "WITH", "VALUES":
		return true
	}
	return false
}

func wrapErr(err error) error {
	m := strings.ToLower(err.Error())
	if strings.Contains(m, "readonly") || strings.Contains(m, "read-only") || strings.Contains(m, "read only") || strings.Contains(m, "query_only") {
		return fmt.Errorf("%w (%v)", errReadOnly, err)
	}
	return err
}

type cell struct {
	s   string
	num bool
}

func toCell(v any) cell {
	switch x := v.(type) {
	case nil:
		return cell{s: "NULL"}
	case int64:
		return cell{strconv.FormatInt(x, 10), true}
	case float64:
		return cell{strconv.FormatFloat(x, 'f', -1, 64), true}
	case bool:
		return cell{strconv.FormatBool(x), false}
	case []byte:
		return cell{s: string(x)}
	case string:
		return cell{s: x}
	}
	return cell{s: fmt.Sprint(v)}
}

func writeCSV(w io.Writer, cols []string, data [][]cell) error {
	cw := csv.NewWriter(w)
	cw.Write(cols)
	for _, r := range data {
		rec := make([]string, len(r))
		for i, c := range r {
			rec[i] = c.s
		}
		cw.Write(rec)
	}
	cw.Flush()
	return cw.Error()
}

// writeTable은 열을 맞춘 표를 쓴다. 머리 아래 ─┼─ 구분선, 숫자는 오른쪽 정렬, 한글은 2칸.
func writeTable(w io.Writer, cols []string, data [][]cell) error {
	width := make([]int, len(cols))
	for i, c := range cols {
		width[i] = dispWidth(c)
	}
	for _, r := range data {
		for i, c := range r {
			if n := dispWidth(c.s); n > width[i] {
				width[i] = n
			}
		}
	}
	var sb strings.Builder
	line := func(cells []cell) {
		for i, c := range cells {
			if i > 0 {
				sb.WriteString(" │ ")
			}
			pad := strings.Repeat(" ", width[i]-dispWidth(c.s))
			switch {
			case c.num:
				sb.WriteString(pad + c.s)
			case i == len(cells)-1:
				sb.WriteString(c.s) // 마지막 열은 뒤 공백을 붙이지 않는다
			default:
				sb.WriteString(c.s + pad)
			}
		}
		sb.WriteByte('\n')
	}
	head := make([]cell, len(cols))
	for i, c := range cols {
		head[i] = cell{s: c}
	}
	line(head)
	for i := range cols {
		if i > 0 {
			sb.WriteString("─┼─")
		}
		sb.WriteString(strings.Repeat("─", width[i]))
	}
	sb.WriteByte('\n')
	for _, r := range data {
		line(r)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

// dispWidth는 터미널 칸 수다: 한글·한자·전각은 2칸, 나머지는 1칸.
func dispWidth(s string) int {
	n := 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Hangul, r), unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r),
			r >= 0xFF01 && r <= 0xFF60, r >= 0x3000 && r <= 0x303F:
			n += 2
		default:
			n++
		}
	}
	return n
}
