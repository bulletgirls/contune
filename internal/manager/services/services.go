// Package services는 서비스다(06, 카드 P-D): services/*.yaml 정의, 프로세스 기록에서 보이는 자동 서비스,
// 서버별 인스턴스 상태, 서비스 시간축의 사건, 에이전트에 내려 줄 정의, 서비스 미기동·선언(starts) 확인 알림.
//
// nil *Book도 안전하다(빈 값).
package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"eum/internal/manager/alert"
	"eum/internal/manager/live"
	"eum/internal/manager/procs"
	"eum/internal/manager/store"
	"eum/internal/model"
)

// Deps는 서비스가 쓰는 부품이다.
type Deps struct {
	Dir    string // services/*.yaml 폴더. 비면 정의 없음(자동 서비스만)
	State  store.StateStore
	Live   *live.Cache
	Procs  *procs.Registry
	Flows  func() []model.Flow
	Panels func() []model.Panel // service: 로 서비스를 가리키는 패널(목록의 Panel 칸)
	Hosts  func() []model.Host  // 그룹(정의의 Group과 맞추기)
	Loc    *time.Location       // Expect.Active를 보는 tz
	Now    func() time.Time

	// ListProcs·LiveProcs는 프로세스 기록 조회와 살아 있는 프로세스(host ""면 모든 서버)를 Procs 대신 준다(시험용).
	// 비면 Procs.List·Procs.Live를 쓴다.
	ListProcs func(ctx context.Context, f model.ProcFilter) ([]model.ProcInfo, error)
	LiveProcs func(host string) []model.ProcInfo
}

// DefError는 읽지 못한 정의 파일 하나다(GET /services의 errors, 다시 읽기 응답). 모양은 패널의 것과 같다.
type DefError struct {
	File string `json:"file"`
	Msg  string `json:"msg"`
}

// Book은 서비스 정의와 상태다. 동시에 불러도 안전하다.
type Book struct {
	d Deps

	mu     sync.Mutex
	defs   []model.ServiceDef      // ID 오름차순
	active map[string]model.Active // 정의 ID → Expect.Active
	errs   []DefError
	conds  []alert.ServiceCond // 마지막으로 계산한 알림 조건(저장소를 못 읽을 때 그대로 돌려줌: 알림이 닫혔다 열리지 않게)
}

// New는 Dir을 읽은 Book이다. 폴더를 못 읽거나 틀린 파일은 로그만 남기고 건너뛴다.
func New(d Deps) *Book {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Loc == nil {
		d.Loc = time.Local
	}
	b := &Book{d: d}
	if _, _, err := b.Reload(); err != nil {
		slog.Error("서비스 정의 폴더를 읽지 못함", "dir", d.Dir, "err", err)
	}
	return b
}

// Reload는 폴더를 다시 읽어 정의를 통째로 바꿔 끼운다. 폴더를 읽지 못하면 err이고 이전 정의를 그대로 쓴다.
func (b *Book) Reload() (n int, errs []DefError, err error) {
	if b == nil {
		return 0, []DefError{}, nil
	}
	defs, errs, err := loadDefs(b.d.Dir)
	if err != nil {
		return 0, nil, err
	}
	for _, e := range errs {
		slog.Warn("서비스 정의 파일을 건너뜀", "dir", b.d.Dir, "file", e.File, "msg", e.Msg)
	}
	act := map[string]model.Active{}
	for _, d := range defs {
		if d.Expect != nil {
			act[d.ID], _ = model.ParseActive(d.Expect.Active) // 읽을 때 검사했다
		}
	}
	if errs == nil {
		errs = []DefError{}
	}
	b.mu.Lock()
	b.defs, b.active, b.errs = defs, act, errs
	b.mu.Unlock()
	return len(defs), errs, nil
}

// loadDefs는 dir의 *.yaml을 이름 순으로 읽는다(파일 하나 = 정의 하나). 틀린 파일과 앞 파일과 ID가 겹치는 파일은
// 건너뛰고 errs에 담는다. dir이 비거나 폴더가 없으면 정의 없음이고, 폴더를 읽지 못하면 err다. 결과는 ID 오름차순.
func loadDefs(dir string) (defs []model.ServiceDef, errs []DefError, err error) {
	if dir == "" {
		return nil, nil, nil
	}
	ents, err := os.ReadDir(dir) // 이름 순
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	seen := map[string]string{} // ID → 파일
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".yaml") {
			continue
		}
		bad := func(msg string) { errs = append(errs, DefError{File: name, Msg: msg}) }
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			var pe *fs.PathError
			if errors.As(err, &pe) {
				err = pe.Err // 경로(파일 이름)는 빼고
			}
			bad("파일을 읽지 못했습니다: " + err.Error())
			continue
		}
		var d model.ServiceDef
		// 모르는 칸은 오류다: 오타가 조용히 무시되면 운영자는 규칙을 걸었다고 믿는다.
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&d); err != nil {
			if err == io.EOF {
				bad("빈 파일입니다")
			} else {
				bad("YAML을 읽지 못함: " + strings.TrimPrefix(err.Error(), "yaml: "))
			}
			continue
		}
		if probs := model.ValidateServiceDef(d); len(probs) > 0 {
			bad(strings.Join(probs, "; "))
			continue
		}
		if first, dup := seen[d.ID]; dup {
			bad(fmt.Sprintf("id %q가 앞 파일 %s에도 있어 건너뜁니다", d.ID, first))
			continue
		}
		seen[d.ID] = name
		if d.Title == "" {
			d.Title = d.ID
		}
		d.File = name
		defs = append(defs, d)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].ID < defs[j].ID })
	return defs, errs, nil
}

// snapshot은 정의·감시 시간·읽지 못한 파일의 지금 모습이다(바꿔 끼우기만 하므로 공유해도 된다).
func (b *Book) snapshot() ([]model.ServiceDef, map[string]model.Active, []DefError) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.defs, b.active, b.errs
}

// Defs는 읽은 정의(검사 통과, ID 오름차순)다.
func (b *Book) Defs() []model.ServiceDef {
	if b == nil {
		return nil
	}
	defs, _, _ := b.snapshot()
	return append([]model.ServiceDef(nil), defs...)
}

// Errors는 읽지 못한 정의 파일이다(늘 배열).
func (b *Book) Errors() []DefError {
	if b == nil {
		return []DefError{}
	}
	_, _, errs := b.snapshot()
	return append([]DefError{}, errs...)
}

// ForAgent는 그 호스트(그룹)에 내려 줄 정의와 그 판(정의가 바뀌면 바뀌는 짧은 문자열)이다. 수집 설정(collect)이 AgentConfig에 싣는다.
// 정의 중 Group이 비거나 group이 그 경로(같거나 아래)인 것이고, 판은 그 정의들(File 뺀 JSON)의 sha256 앞 12자다.
// 내려 줄 정의가 없으면 nil, ""(판을 섞지 않음).
func (b *Book) ForAgent(host, group string) ([]model.ServiceDef, string) {
	if b == nil {
		return nil, ""
	}
	defs, _, _ := b.snapshot()
	var out []model.ServiceDef
	for _, d := range defs {
		if d.Group == "" || group == d.Group || strings.HasPrefix(group, d.Group+"/") {
			d.File = "" // 에이전트에는 필요 없고, 파일 이름만 바뀌어도 판이 바뀌지 않게
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return nil, ""
	}
	raw, err := json.Marshal(out)
	if err != nil { // 문자열·수·참거짓뿐이라 실패하지 않는다
		return out, ""
	}
	sum := sha256.Sum256(raw)
	return out, hex.EncodeToString(sum[:])[:12]
}
