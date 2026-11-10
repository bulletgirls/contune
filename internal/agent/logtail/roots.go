package logtail

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// read_roots(에이전트 YAML remote.read_roots): 매니저 화면에서 받은 원격 출처와 미리보기가 읽어도 되는 폴더.
// 판정은 언제나 실제 경로(filepath.EvalSymlinks)로 하고, 여는 것은 os.Root로 해서
// 판정과 열기 사이에 심볼릭 링크로 바꿔치기해도 폴더 밖을 열지 못한다.

// RealPath는 p의 실제 경로다(절대 경로, 심볼릭 링크를 따라감). 아직 없는 끝부분은
// 있는 조상의 실제 경로 뒤에 글자 그대로 붙인다(만들어질 폴더·파일의 판정용).
func RealPath(p string) (string, error) {
	p = absDir(p)
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", err
	}
	rp, err := RealPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(rp, filepath.Base(p)), nil
}

// within은 실제 경로 p가 실제 경로 root 안(같은 경로 포함)인지 본다.
func within(p, root string) bool {
	if p == root || root == string(filepath.Separator) {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// rootOf는 실제 경로 p를 품은 read_roots 폴더의 실제 경로다. 없으면 "".
func rootOf(roots []string, p string) string {
	for _, r := range roots {
		if r == "" {
			continue
		}
		rr, err := RealPath(r)
		if err != nil {
			continue
		}
		if within(p, rr) {
			return rr
		}
	}
	return ""
}

// InRoots는 p의 실제 경로가 roots 중 하나의 안인지 본다. roots가 비면 언제나 false.
func InRoots(roots []string, p string) bool {
	real, err := RealPath(p)
	if err != nil {
		return false
	}
	return rootOf(roots, real) != ""
}

// ErrOutsideRoots는 실제 경로가 read_roots 밖이라 열지 않았다는 뜻이다.
var ErrOutsideRoots = errors.New("read_roots 밖 경로입니다(에이전트 YAML remote.read_roots)")

// OpenIn은 실제 경로가 roots 안인 일반 파일만 읽기 전용으로 연다.
func OpenIn(roots []string, p string) (*os.File, error) {
	real, err := filepath.EvalSymlinks(absDir(p))
	if err != nil {
		return nil, err
	}
	rr := rootOf(roots, real)
	if rr == "" {
		return nil, ErrOutsideRoots
	}
	rel, err := filepath.Rel(rr, real)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rr)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel) // 그사이 바꿔치기한 링크가 밖을 가리키면 os.Root가 거절한다
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s: 일반 파일이 아닙니다", p)
	}
	return f, nil
}
