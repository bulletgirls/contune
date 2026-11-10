package collect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"eum/internal/model"
)

// FileError는 collect 폴더에서 읽지 못한(건너뛴) 파일 하나다.
type FileError struct {
	File string `json:"file"`
	Msg  string `json:"msg"`
}

// fileDoc은 collect 폴더 YAML 파일 하나의 모양이다.
type fileDoc struct {
	Scope string            `yaml:"scope"`
	Logs  []model.LogSource `yaml:"logs"`
}

const (
	maxFile    = 1 << 20 // 설정 파일 하나의 크기
	fileHeader = "# 이음 수집 설정 — 화면(#collect)에서 저장하면 이 파일을 다시 쓴다. 손으로 고쳤으면 POST /api/v1/collect/reload"
)

// shaOf는 바이트의 sha256 hex 앞 16자다(편집기 shaOf와 같은 방식).
func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])[:16]
}

// loadDir는 dir의 *.yaml·*.yml(하위 폴더·.으로 시작하는 파일은 안 봄)을 이름 순으로 읽는다. 파일 하나 = 범위 하나.
// 범위 문법·model.ValidateLogSources를 통과하지 못한 파일, 이미 나온 범위를 또 쓴 파일은 건너뛰고 errors에 담는다.
// 폴더가 없으면 빈 설정이다. 폴더를 읽지 못하면 에러.
func loadDir(dir string) (map[string]model.CollectSet, []FileError, error) {
	sets := map[string]model.CollectSet{}
	errs := []FileError{}
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return sets, errs, nil
	}
	if err != nil {
		return nil, nil, err
	}
	for _, ent := range ents {
		name := ent.Name()
		if ent.IsDir() || strings.HasPrefix(name, ".") || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		set, err := readSet(filepath.Join(dir, name))
		if err == nil {
			if prev, dup := sets[set.Scope]; dup {
				err = fmt.Errorf("범위 %s는 %s가 이미 씁니다", set.Scope, prev.File)
			}
		}
		if err != nil {
			errs = append(errs, FileError{File: name, Msg: err.Error()})
			continue
		}
		set.File = name
		sets[set.Scope] = set
	}
	return sets, errs, nil
}

// readSet은 파일 하나를 읽어 검사한다(File은 비워 둔다).
func readSet(path string) (model.CollectSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return model.CollectSet{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return model.CollectSet{}, err
	}
	if len(b) > maxFile {
		return model.CollectSet{}, fmt.Errorf("파일이 %d바이트를 넘습니다", maxFile)
	}
	var doc fileDoc
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return model.CollectSet{}, errors.New("파일이 비어 있습니다")
		}
		return model.CollectSet{}, fmt.Errorf("YAML을 해석하지 못함: %v", err)
	}
	if err := checkScope(doc.Scope); err != nil {
		return model.CollectSet{}, err
	}
	if ps := model.ValidateLogSources(doc.Logs); len(ps) > 0 {
		msgs := make([]string, len(ps))
		for i, p := range ps {
			msgs[i] = p.String()
		}
		return model.CollectSet{}, errors.New(strings.Join(msgs, "; "))
	}
	if doc.Logs == nil {
		doc.Logs = []model.LogSource{}
	}
	return model.CollectSet{Scope: doc.Scope, SHA: shaOf(b), Logs: doc.Logs}, nil
}

// encodeFile은 범위 하나를 파일 내용으로 쓴다: 머리 주석, scope, logs(규칙은 한 줄 흐름 모양).
func encodeFile(scope string, logs []model.LogSource) ([]byte, error) {
	if logs == nil {
		logs = []model.LogSource{}
	}
	var root yaml.Node
	if err := root.Encode(fileDoc{Scope: scope, Logs: logs}); err != nil {
		return nil, err
	}
	root.HeadComment = fileHeader
	for _, src := range valueOf(&root, "logs").Content {
		for _, m := range valueOf(src, "match").Content {
			m.Style = yaml.FlowStyle
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// valueOf는 매핑 노드에서 key의 값 노드다(없으면 빈 노드).
func valueOf(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return &yaml.Node{}
}

// writeAtomic은 같은 폴더의 임시 파일에 쓰고 rename한다. 폴더가 없으면 만든다.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".eum-collect-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o644)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}
