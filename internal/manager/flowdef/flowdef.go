// Package flowdef는 플로우 정의(flows/<배치판>/*.yaml) 로더와 레지스트리다(02 §9, 05 §8).
package flowdef

import (
	"context"
	"errors"
	"sync"
	"time"

	"eum/internal/model"
)

var errNotImplemented = errors.New("not implemented")

// Load는 dir 아래 YAML을 모두 읽는다. 폴더 경로가 배치판이다. (stub: B1이 채움)
func Load(dir string) ([]model.Flow, error) { return nil, nil }

// Parse는 YAML 하나를 플로우로 읽는다. (stub)
func Parse(data []byte, file, board string) (model.Flow, error) {
	return model.Flow{}, errNotImplemented
}

// Validate는 플로우 목록 전체(순환, id 중복, 선행 이름 등)를 검사한다. (stub)
func Validate(flows []model.Flow) error { return errNotImplemented }

// Marshal은 플로우를 YAML로 쓴다. (stub)
func Marshal(f model.Flow) ([]byte, error) { return nil, errNotImplemented }

// Registry는 지금 쓰는 플로우 정의 목록이다(동시 사용 안전). (stub: 빈 목록)
type Registry struct {
	mu      sync.RWMutex
	dir     string
	flows   []model.Flow
	errs    []string
	version int64
}

// NewRegistry는 dir을 읽어 레지스트리를 만든다. 읽기 실패는 Errors()에 두고 에러로 돌려주지 않는다.
func NewRegistry(dir string) (*Registry, error) {
	return &Registry{dir: dir}, nil
}

// List는 플로우 목록이다.
func (r *Registry) List() []model.Flow {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.flows
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

// Reload는 파일을 다시 읽는다. (stub)
func (r *Registry) Reload() error { return nil }

// Watch는 every마다 파일 변경을 보고 다시 읽는다. ctx가 끝날 때까지 돈다. (stub: 대기)
func (r *Registry) Watch(ctx context.Context, every time.Duration) {
	<-ctx.Done()
}

// Errors는 마지막 읽기의 에러 목록이다.
func (r *Registry) Errors() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.errs
}

// Dir은 flows 폴더다.
func (r *Registry) Dir() string { return r.dir }

// Version은 목록이 바뀔 때마다 1씩 오른다.
func (r *Registry) Version() int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}
