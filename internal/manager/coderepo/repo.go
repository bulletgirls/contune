package coderepo

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"eum/internal/model"
)

const (
	maxFiles    = 20000   // 저장소 하나에서 읽는 파일 수 한도
	maxFileSize = 1 << 20 // 이보다 큰 파일은 읽지 않는다
	binSniff    = 8 << 10 // 앞 8KB에 NUL이 있으면 이진 파일
	lineMax     = 300     // 줄 하나의 글자 수 한도
	checkEvery  = 30 * time.Second
)

// repo는 저장소 하나다. 색인은 게으르게 만들고, HEAD·폴더 변경을 checkEvery에 한 번 확인해 바뀌면 다시 만든다.
type repo struct {
	id      string
	root    string // 심볼릭 링크를 푼 절대 경로
	f       filter
	include []string // git pathspec용 원본 패턴
	exclude []string
	match   []string
	web     string
	git     *gitRunner // nil이면 git 없음
	every   time.Duration
	now     func() time.Time

	mu      sync.Mutex
	checked time.Time
	sig     string
	idx     *index
	capped  bool
	hist    map[string][]string
}

// index는 한 번 훑은 저장소다.
type index struct {
	head   string           // HEAD 전체 hash(git이 아니거나 커밋이 없으면 "")
	files  []string         // 읽을 수 있는 파일(정렬)
	byName map[string][]loc // 함수 이름 → 정의
	byFile map[string][]fn  // 파일 → 함수(start 순)
}

type loc struct {
	file string
	fn   fn
}

// cleanRel은 저장소 안 상대 경로를 다듬는다. 절대 경로·..·NUL·빈 경로는 거절.
func cleanRel(p string) (string, bool) {
	if p == "" || strings.IndexByte(p, 0) >= 0 || strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return "", false
	}
	for _, s := range strings.Split(p, "/") {
		if s == ".." {
			return "", false
		}
	}
	c := path.Clean(p)
	if c == "." || c == "" || strings.HasPrefix(c, "../") {
		return "", false
	}
	return c, true
}

// inside는 실제 경로가 저장소 안이면 저장소 안 경로를 준다.
func (r *repo) inside(real string) (string, bool) {
	rel, err := filepath.Rel(r.root, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// resolve는 읽을 수 있는 파일의 저장소 안 경로와 실제 경로다: 경로 규칙, 심볼릭 링크를 따라간 곳이 저장소 안이고
// 그곳도 규칙 안, 보통 파일, 크기 한도. 아니면 ErrNotFound. 링크면 따라간 곳의 경로를 준다(같은 파일을 두 이름으로 세지 않게).
func (r *repo) resolve(rel string) (string, string, error) {
	rel, ok := cleanRel(rel)
	if !ok || !r.f.fileOK(rel) {
		return "", "", ErrNotFound
	}
	real, err := filepath.EvalSymlinks(filepath.Join(r.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", "", ErrNotFound
	}
	rr, ok := r.inside(real)
	if !ok || !r.f.fileOK(rr) {
		return "", "", ErrNotFound
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFileSize {
		return "", "", ErrNotFound
	}
	return rr, real, nil
}

// readFile은 읽을 수 있는 파일의 내용이다(이진 파일은 ErrNotFound).
func (r *repo) readFile(rel string) (string, []byte, error) {
	rel, real, err := r.resolve(rel)
	if err != nil {
		return "", nil, err
	}
	b, err := os.ReadFile(real)
	if err != nil || len(b) > maxFileSize || isBinary(b) {
		return "", nil, ErrNotFound
	}
	return rel, b, nil
}

func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), binSniff)], 0) >= 0
}

type entry struct {
	rel, real string
}

// scan은 경로 규칙 안의 보통 파일을 훑어 지문(HEAD + 경로·크기·시각)과 목록을 준다. maxFiles에서 멈춘다.
// 심볼릭 링크는 목록에 넣지 않는다: 저장소 안을 가리키면 그 파일이 이미 목록에 있고, 밖을 가리키면 읽지 않는다.
func (r *repo) scan(head string) (string, []entry, bool) {
	h := fnv.New128a()
	h.Write([]byte(head))
	var ents []entry
	capped := false
	_ = filepath.WalkDir(r.root, func(p string, d fs.DirEntry, err error) error {
		if p == r.root {
			return err
		}
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, ok := r.inside(p)
		if !ok {
			return nil
		}
		if d.IsDir() {
			if !r.f.dirOK(rel) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !r.f.fileOK(rel) {
			return nil
		}
		fi, err := d.Info()
		if err != nil || fi.Size() > maxFileSize {
			return nil
		}
		if len(ents) >= maxFiles {
			capped = true
			return fs.SkipAll
		}
		ents = append(ents, entry{rel, p})
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", rel, fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), ents, capped
}

// refresh는 지금 색인이다. 마지막 확인에서 every가 지났으면 HEAD·폴더를 확인해 바뀌었으면 다시 만든다.
// 확인은 부른 쪽의 취소와 상관없이 끝낸다(다른 호출자가 같은 색인을 쓴다).
func (r *repo) refresh(ctx context.Context) *index {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if r.idx != nil && now.Sub(r.checked) < r.every {
		return r.idx
	}
	r.checked = now
	head := r.gitHead(context.WithoutCancel(ctx))
	sig, ents, capped := r.scan(head)
	if r.idx != nil && sig == r.sig {
		return r.idx
	}
	if capped && !r.capped {
		slog.Warn("코드 저장소 파일이 너무 많아 앞의 것만 읽음", "repo", r.id, "limit", maxFiles)
	}
	if r.idx == nil || head != r.idx.head {
		r.hist = nil
	}
	r.sig, r.capped = sig, capped
	r.idx = buildIndex(head, ents)
	return r.idx
}

func buildIndex(head string, ents []entry) *index {
	idx := &index{head: head, byName: map[string][]loc{}, byFile: map[string][]fn{}}
	for _, e := range ents {
		b, err := os.ReadFile(e.real)
		if err != nil || len(b) > maxFileSize || isBinary(b) {
			continue
		}
		idx.files = append(idx.files, e.rel)
		fns := parseFuncs(e.rel, b)
		if len(fns) == 0 {
			continue
		}
		sort.SliceStable(fns, func(i, j int) bool { return fns[i].start < fns[j].start })
		idx.byFile[e.rel] = fns
		for _, f := range fns {
			idx.byName[f.name] = append(idx.byName[f.name], loc{e.rel, f})
		}
	}
	sort.Strings(idx.files)
	return idx
}

// clip은 줄 하나를 lineMax 글자로 자른다(잘리면 끝이 …).
func clip(s string) string {
	if utf8.RuneCountInString(s) <= lineMax {
		return s
	}
	r := []rune(s)
	return string(r[:lineMax-1]) + "…"
}

// maskLine은 화면·LLM으로 나가는 줄이다: 비밀 가림, 글자 수 한도, 깨진 UTF-8 정리.
func maskLine(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return clip(model.MaskSecrets(s))
}
