// Package sqlcli는 eum sql이다: 두 DB를 읽기 전용으로 열어 SQL 조회를 한다(02 §6.1).
package sqlcli

import (
	"errors"
	"io"
)

// Run은 dataDir의 metrics.db를 읽기 전용으로 열고 state.db를 붙여 query를 실행해 w에 찍는다. (stub: C5가 채움)
func Run(dataDir, query string, csv bool, w io.Writer) error {
	return errors.New("not implemented")
}
