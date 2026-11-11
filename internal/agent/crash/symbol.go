package crash

import (
	"debug/elf"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"eum/internal/model"
)

// debugRoot는 분리된 디버그 파일(.build-id/xx/yyyy.debug)을 찾는 곳이다. 배포판 libc는 .symtab을 떼어 여기 둔다.
var debugRoot = "/usr/lib/debug"

type symbol struct {
	value, size uint64
	name        string
}

// module은 core의 NT_FILE에 나온 파일 하나(실행 파일·라이브러리)다. 디스크의 ELF는 처음 쓸 때 연다.
type module struct {
	path   string // 디스크 경로(" (deleted)" 뗌)
	name   string // 파일 이름만
	maps   []mapping
	opened bool
	f      *os.File
	ef     *elf.File
	bias   uint64 // 실제 주소 - ELF 주소
	syms   []symbol
}

type symbolizer struct {
	core *coreFile
	mods []*module
}

func newSymbolizer(c *coreFile) *symbolizer {
	s := &symbolizer{core: c}
	byName := map[string]*module{}
	for _, m := range c.notes.files {
		mod := byName[m.name]
		if mod == nil {
			p := trimDeleted(m.name)
			mod = &module{path: p, name: filepath.Base(p)}
			byName[m.name] = mod
			s.mods = append(s.mods, mod)
		}
		mod.maps = append(mod.maps, m)
	}
	return s
}

func (s *symbolizer) close() {
	for _, m := range s.mods {
		if m.f != nil {
			_ = m.f.Close()
		}
	}
}

func (s *symbolizer) moduleOf(addr uint64) *module {
	for _, m := range s.mods {
		for _, mp := range m.maps {
			if addr >= mp.start && addr < mp.end {
				return m
			}
		}
	}
	return nil
}

// exeModule은 실행 파일의 모듈이다: AT_ENTRY를 품은 것, 없으면 p.Exe와 같은 경로, 없으면 가장 낮은 주소.
func (s *symbolizer) exeModule(p Proc) *module {
	if e := s.core.notes.entry; e != 0 {
		if m := s.moduleOf(e); m != nil {
			return m
		}
	}
	if exe := trimDeleted(p.Exe); exe != "" {
		for _, m := range s.mods {
			if m.path == exe {
				return m
			}
		}
	}
	var low *module
	for _, m := range s.mods {
		if low == nil || m.base() < low.base() {
			low = m
		}
	}
	return low
}

// base는 이 모듈의 가장 낮은 매핑 시작이다.
func (m *module) base() uint64 {
	b := m.maps[0].start
	for _, mp := range m.maps {
		b = min(b, mp.start)
	}
	return b
}

// headerAddr는 파일 위치 0(ELF 머리)이 매핑된 주소다. 없으면 0.
func (m *module) headerAddr() uint64 {
	for _, mp := range m.maps {
		if mp.off == 0 {
			return mp.start
		}
	}
	return 0
}

func (m *module) load() {
	if m.opened {
		return
	}
	m.opened = true
	f, err := os.Open(m.path)
	if err != nil {
		return
	}
	ef, err := elf.NewFile(f)
	if err != nil {
		_ = f.Close()
		return
	}
	m.f, m.ef = f, ef
	m.bias = loadBias(m.maps, ef)
	m.syms = loadSymbols(ef)
}

// loadBias는 가장 낮은 매핑과 그 파일 위치를 덮는 PT_LOAD로 bias(실제 주소 - ELF 가상 주소)를 구한다.
// ET_EXEC(PIE 아님)이면 0이 나온다.
func loadBias(maps []mapping, ef *elf.File) uint64 {
	low := maps[0]
	for _, mp := range maps {
		if mp.start < low.start {
			low = mp
		}
	}
	for _, p := range ef.Progs {
		if p.Type == elf.PT_LOAD && low.off >= p.Off&^0xfff && low.off < p.Off+p.Filesz {
			return low.start - (p.Vaddr + low.off - p.Off)
		}
	}
	return low.start - low.off
}

// loadSymbols는 함수 심볼을 주소순으로 준다(.symtab, 없으면 분리된 디버그 파일의 .symtab, 그리고 .dynsym).
// 같은 주소의 별명은 하나만: 크기가 있는 것, GLOBAL, 앞의 밑줄이 적은 이름을 고른다.
func loadSymbols(ef *elf.File) []symbol {
	var all []elf.Symbol
	if ss, err := ef.Symbols(); err == nil {
		all = append(all, ss...)
	} else if dbg := openDebugFile(ef); dbg != nil {
		if ss, err := dbg.Symbols(); err == nil {
			all = append(all, ss...)
		}
		_ = dbg.Close()
	}
	if ds, err := ef.DynamicSymbols(); err == nil {
		all = append(all, ds...)
	}
	rank := func(s elf.Symbol) int {
		r := 0
		if s.Size > 0 {
			r += 100
		}
		switch elf.ST_BIND(s.Info) {
		case elf.STB_GLOBAL:
			r += 20
		case elf.STB_WEAK:
			r += 10
		}
		return r - (len(s.Name) - len(strings.TrimLeft(s.Name, "_")))
	}
	var fs []elf.Symbol
	for _, s := range all {
		if i := strings.IndexByte(s.Name, '@'); i > 0 {
			s.Name = s.Name[:i] // .symtab의 "pthread_kill@@GLIBC_2.34"
		}
		t := elf.ST_TYPE(s.Info)
		if (t == elf.STT_FUNC || t == elf.STT_GNU_IFUNC) && s.Section != elf.SHN_UNDEF && s.Value != 0 && s.Name != "" {
			fs = append(fs, s)
		}
	}
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Value != fs[j].Value {
			return fs[i].Value < fs[j].Value
		}
		return rank(fs[i]) > rank(fs[j])
	})
	out := make([]symbol, 0, len(fs))
	for _, s := range fs {
		if len(out) > 0 && out[len(out)-1].value == s.Value {
			continue
		}
		out = append(out, symbol{value: s.Value, size: s.Size, name: s.Name})
	}
	return out
}

func openDebugFile(ef *elf.File) *elf.File {
	id := elfBuildID(ef)
	if len(id) < 3 {
		return nil
	}
	f, err := elf.Open(filepath.Join(debugRoot, ".build-id", id[:2], id[2:]+".debug"))
	if err != nil {
		return nil
	}
	return f
}

// lookup은 ELF 주소 v를 품은 함수다. 크기가 없는 심볼은 64KiB 안쪽만 믿는다.
func (m *module) lookup(v uint64) (symbol, bool) {
	i := sort.Search(len(m.syms), func(i int) bool { return m.syms[i].value > v }) - 1
	if i < 0 {
		return symbol{}, false
	}
	s := m.syms[i]
	if s.size > 0 && v-s.value >= s.size || s.size == 0 && v-s.value > 1<<16 {
		return symbol{}, false
	}
	return s, true
}

// frame은 주소 하나를 칸으로 만든다. isReturn이면 돌아갈 주소라 1을 빼고 함수를 찾는다
// (noreturn 호출이 함수 끝이면 돌아갈 주소가 다음 함수를 가리킨다). Offset은 gdb의 <f+n>처럼 주소 그대로의 거리.
func (s *symbolizer) frame(addr uint64, isReturn bool) model.Frame {
	f := model.Frame{Func: "??", Module: "??", Addr: hexAddr(addr)}
	m := s.moduleOf(addr)
	if m == nil {
		return f
	}
	f.Module = m.name
	m.load()
	if m.ef == nil {
		return f
	}
	look := addr - m.bias
	if isReturn {
		look--
	}
	if sym, ok := m.lookup(look); ok {
		f.Func = sym.name
		f.Offset = fmt.Sprintf("+0x%x", addr-m.bias-sym.value)
	}
	return f
}

// code는 주소 addr부터 n바이트의 기계어다. 읽기 전용 .text는 core에 없으므로 디스크의 ELF에서 읽고,
// 거기 없으면 core에서 읽는다.
func (s *symbolizer) code(addr uint64, n int) []byte {
	if m := s.moduleOf(addr); m != nil {
		m.load()
		if m.ef != nil {
			v := addr - m.bias
			for _, p := range m.ef.Progs {
				if p.Type == elf.PT_LOAD && v >= p.Vaddr && v+uint64(n) <= p.Vaddr+p.Filesz {
					buf := make([]byte, n)
					if _, err := p.ReadAt(buf, int64(v-p.Vaddr)); err == nil {
						return buf
					}
				}
			}
		}
	}
	if b := s.core.readMem(addr, n); len(b) == n {
		return b
	}
	return nil
}
