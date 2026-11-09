package flowdef

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"eum/internal/model"
)

// Registry는 지금 쓰는 플로우 정의 목록이다(동시 사용 안전).
type Registry struct {
	mu      sync.RWMutex
	dir     string
	flows   []model.Flow
	errs    []string
	version int64
	sig     string
}

// NewRegistry는 dir을 읽어 레지스트리를 만든다. 읽기 실패는 Errors()에 두고 에러로 돌려주지 않는다.
func NewRegistry(dir string) (*Registry, error) {
	r := &Registry{dir: dir}
	r.sig = r.signature()
	if err := r.Reload(); err != nil {
		slog.Warn("플로우 정의 읽기 실패", "err", err)
	}
	r.mu.Lock()
	r.version = 0
	r.mu.Unlock()
	return r, nil
}

// List는 플로우 목록의 복사본이다.
func (r *Registry) List() []model.Flow {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]model.Flow(nil), r.flows...)
}

// Get은 id의 플로우다.
func (r *Registry) Get(id string) (model.Flow, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, f := range r.flows {
		if f.ID == id {
			return f, true
		}
	}
	return model.Flow{}, false
}

// Reload는 파일을 다시 읽는다. Load가 성공할 때만 목록을 바꾼다.
func (r *Registry) Reload() error {
	sig := r.signature()
	flows, err := Load(r.dir)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sig = sig
	if err != nil {
		r.errs = strings.Split(err.Error(), "\n")
		return err
	}
	r.errs = nil
	r.flows = flows
	r.version++
	return nil
}

// Watch는 every마다 파일 변경을 보고 다시 읽는다. ctx가 끝날 때까지 돈다.
func (r *Registry) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.mu.RLock()
			old := r.sig
			r.mu.RUnlock()
			if r.signature() != old {
				if err := r.Reload(); err != nil {
					slog.Warn("플로우 정의 다시 읽기 실패, 이전 정의 유지", "err", err)
				}
			}
		}
	}
}

// signature는 폴더 안 YAML의 (경로, 크기, 수정 시각) 목록을 문자열로 만든다.
func (r *Registry) signature() string {
	var parts []string
	_ = filepath.WalkDir(r.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != r.dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}
		if info, err := d.Info(); err == nil {
			parts = append(parts, fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()))
		}
		return nil
	})
	sort.Strings(parts)
	return strings.Join(parts, "\n")
}

// Errors는 마지막 읽기의 에러 목록이다.
func (r *Registry) Errors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.errs...)
}

// Dir은 flows 폴더다.
func (r *Registry) Dir() string { return r.dir }

// Version은 다시 읽을 때마다 1씩 오른다.
func (r *Registry) Version() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}
