package crash

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// core note 종류(include/uapi/linux/elf.h). 이름이 "CORE"인 것만 읽는다.
const (
	ntPRSTATUS = 1
	ntPRPSINFO = 3
	ntAUXV     = 6
	ntSIGINFO  = 0x53494749 // "SIGI"
	ntFILE     = 0x46494c45 // "FILE"

	ntGNUBuildID = 3 // 이름 "GNU"
	atEntry      = 9 // auxv AT_ENTRY
)

// x86-64 user_regs_struct의 위치.
const (
	regRIP   = 16
	regRSP   = 19
	numRegs  = 27
	maxNotes = 64 << 20 // PT_NOTE를 이보다 많이 읽지 않는다
)

type thread struct {
	tid    int
	cursig int
	regs   []uint64 // 64비트 core면 user_regs_struct 27개(x86-64 기준 위치)
}

type sigInfo struct {
	signo, errno, code int
	addr               uint64 // si_code > 0일 때 결함 주소
	pid, uid           int    // si_code ≤ 0일 때 보낸 쪽
}

// mapping은 NT_FILE의 한 줄이다. off는 파일 안 바이트 위치.
type mapping struct {
	start, end, off uint64
	name            string
}

type psInfo struct {
	pid, uid, gid int
	fname, args   string
}

type coreNotes struct {
	threads []thread // 첫 칸이 core를 만든(신호를 받은) 스레드
	sig     *sigInfo
	files   []mapping
	ps      *psInfo
	entry   uint64 // AT_ENTRY: 실행 파일을 가려낼 때 쓴다
}

func align4(n uint64) uint64 { return (n + 3) &^ 3 }

// eachNote는 note 묶음을 차례로 넘긴다. 잘리거나 크기가 터무니없는 note를 만나면 멈춘다.
func eachNote(data []byte, bo binary.ByteOrder, fn func(name string, typ uint32, desc []byte)) {
	for len(data) >= 12 {
		namesz := uint64(bo.Uint32(data[0:]))
		descsz := uint64(bo.Uint32(data[4:]))
		typ := bo.Uint32(data[8:])
		descOff := 12 + align4(namesz)
		end := descOff + descsz
		if end > uint64(len(data)) {
			return
		}
		fn(strings.TrimRight(string(data[12:12+namesz]), "\x00"), typ, data[descOff:end])
		next := descOff + align4(descsz)
		if next >= uint64(len(data)) {
			return
		}
		data = data[next:]
	}
}

// parseNotes는 core의 PT_NOTE 내용에서 스레드·신호·매핑·프로세스 정보를 뽑는다.
func parseNotes(data []byte, bo binary.ByteOrder, class elf.Class) coreNotes {
	var n coreNotes
	w := 8
	if class == elf.ELFCLASS32 {
		w = 4
	}
	eachNote(data, bo, func(name string, typ uint32, desc []byte) {
		if name == "CORE" {
			n.add(typ, desc, bo, w)
		}
	})
	return n
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func (n *coreNotes) add(typ uint32, d []byte, bo binary.ByteOrder, w int) {
	word := func(off int) uint64 {
		if w == 8 {
			return bo.Uint64(d[off:])
		}
		return uint64(bo.Uint32(d[off:]))
	}
	switch typ {
	case ntPRSTATUS:
		// elf_prstatus: pr_info(3×int) pr_cursig(short)+채움, pr_sigpend·pr_sighold(long), pr_pid·ppid·pgrp·sid(int),
		// 시간 넷(timeval = long 둘), pr_reg. 64비트: pid 32, reg 112.
		pidOff := 16 + 2*w
		if len(d) < pidOff+4 {
			return
		}
		t := thread{cursig: int(bo.Uint16(d[12:])), tid: int(int32(bo.Uint32(d[pidOff:])))}
		regOff := pidOff + 16 + 8*w
		if w == 8 && len(d) >= regOff+numRegs*8 {
			t.regs = make([]uint64, numRegs)
			for i := range t.regs {
				t.regs[i] = bo.Uint64(d[regOff+8*i:])
			}
		}
		n.threads = append(n.threads, t)
	case ntSIGINFO:
		u := 16 // 공용체는 64비트에서 16, 32비트에서 12
		if w == 4 {
			u = 12
		}
		if n.sig != nil || len(d) < u+8 {
			return
		}
		n.sig = &sigInfo{
			signo: int(int32(bo.Uint32(d[0:]))),
			errno: int(int32(bo.Uint32(d[4:]))),
			code:  int(int32(bo.Uint32(d[8:]))),
			addr:  word(u),
			pid:   int(int32(bo.Uint32(d[u:]))),
			uid:   int(bo.Uint32(d[u+4:])),
		}
	case ntFILE:
		// count, page_size, (start, end, file_ofs 페이지) × count, 이름들(NUL 구분)
		if len(d) < 2*w {
			return
		}
		count, page := word(0), word(w)
		if count > uint64((len(d)-2*w)/(3*w)) {
			return
		}
		names := bytes.Split(d[2*w+int(count)*3*w:], []byte{0})
		for i := 0; i < int(count) && i < len(names); i++ {
			b := 2*w + i*3*w
			n.files = append(n.files, mapping{start: word(b), end: word(b + w), off: word(b+2*w) * page, name: string(names[i])})
		}
	case ntPRPSINFO:
		if w != 8 || len(d) < 136 {
			return
		}
		n.ps = &psInfo{
			uid:   int(bo.Uint32(d[16:])),
			gid:   int(bo.Uint32(d[20:])),
			pid:   int(int32(bo.Uint32(d[24:]))),
			fname: cstr(d[40:56]),
			args:  strings.TrimSpace(cstr(d[56:136])),
		}
	case ntAUXV:
		for off := 0; off+2*w <= len(d); off += 2 * w {
			if word(off) == atEntry {
				n.entry = word(off + w)
			}
		}
	}
}

// coreFile은 열어 둔 core다. 메모리는 PT_LOAD로 읽는다.
type coreFile struct {
	r         io.ReaderAt
	size      int64
	bo        binary.ByteOrder
	class     elf.Class
	machine   elf.Machine
	loads     []*elf.Prog
	truncated bool
	wantSize  uint64 // PT_LOAD·PT_NOTE가 가리키는 파일 끝(잘리지 않았다면의 크기)
	notes     coreNotes
}

func openCore(r io.ReaderAt, size int64) (*coreFile, error) {
	ef, err := elf.NewFile(r)
	if err != nil {
		return nil, fmt.Errorf("ELF가 아님: %w", err)
	}
	if ef.Type != elf.ET_CORE {
		return nil, fmt.Errorf("core 파일이 아님(%s)", ef.Type)
	}
	c := &coreFile{r: r, size: size, bo: ef.ByteOrder, class: ef.Class, machine: ef.Machine}
	for _, p := range ef.Progs {
		if p.Type != elf.PT_LOAD && p.Type != elf.PT_NOTE {
			continue
		}
		if end := p.Off + p.Filesz; end > c.wantSize {
			c.wantSize = end
		}
		if p.Off+p.Filesz > uint64(size) {
			c.truncated = true
		}
		if p.Type == elf.PT_LOAD {
			c.loads = append(c.loads, p)
			continue
		}
		n := p.Filesz
		if p.Off >= uint64(size) {
			continue
		}
		n = min(n, uint64(size)-p.Off, maxNotes)
		buf := make([]byte, n)
		m, _ := r.ReadAt(buf, int64(p.Off))
		c.notes.merge(parseNotes(buf[:m], c.bo, c.class))
	}
	return c, nil
}

func (n *coreNotes) merge(o coreNotes) {
	n.threads = append(n.threads, o.threads...)
	n.files = append(n.files, o.files...)
	if n.sig == nil {
		n.sig = o.sig
	}
	if n.ps == nil {
		n.ps = o.ps
	}
	if n.entry == 0 {
		n.entry = o.entry
	}
}

// pid는 core 안의 프로세스 PID다(PRPSINFO, 없으면 첫 스레드). 모르면 0.
func (c *coreFile) pid() int {
	if c.notes.ps != nil && c.notes.ps.pid > 0 {
		return c.notes.ps.pid
	}
	if len(c.notes.threads) > 0 {
		return c.notes.threads[0].tid
	}
	return 0
}

// readMem은 프로세스 주소 addr부터 n바이트까지를 core에서 읽는다. core에 없으면(덤프 제외·잘림) 짧거나 nil.
func (c *coreFile) readMem(addr uint64, n int) []byte {
	for _, p := range c.loads {
		if addr < p.Vaddr || addr-p.Vaddr >= p.Memsz {
			continue
		}
		rel := addr - p.Vaddr
		if rel >= p.Filesz {
			return nil
		}
		if avail := p.Filesz - rel; uint64(n) > avail {
			n = int(avail)
		}
		buf := make([]byte, n)
		m, _ := c.r.ReadAt(buf, int64(p.Off+rel))
		return buf[:m]
	}
	return nil
}

// executable은 addr가 실행 권한이 있는 매핑 안인가다(PT_LOAD의 PF_X. 내용이 덤프되지 않아도 머리는 있다).
func (c *coreFile) executable(addr uint64) bool {
	for _, p := range c.loads {
		if addr >= p.Vaddr && addr-p.Vaddr < p.Memsz {
			return p.Flags&elf.PF_X != 0
		}
	}
	return false
}

// corePID는 path가 ELF core인지와 그 안의 PID다(Locate의 후보 거르기).
func corePID(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, false
	}
	c, err := openCore(f, fi.Size())
	if err != nil {
		return 0, false
	}
	return c.pid(), true
}

// findBuildID는 note 묶음에서 GNU build-id를 hex로 찾는다.
func findBuildID(data []byte, bo binary.ByteOrder) string {
	id := ""
	eachNote(data, bo, func(name string, typ uint32, desc []byte) {
		if id == "" && name == "GNU" && typ == ntGNUBuildID && len(desc) > 0 {
			id = hex.EncodeToString(desc)
		}
	})
	return id
}

func buildID(path string) string {
	f, err := elf.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	return elfBuildID(f)
}

func elfBuildID(f *elf.File) string {
	if s := f.Section(".note.gnu.build-id"); s != nil {
		if d, err := s.Data(); err == nil {
			if id := findBuildID(d, f.ByteOrder); id != "" {
				return id
			}
		}
	}
	for _, p := range f.Progs {
		if p.Type != elf.PT_NOTE {
			continue
		}
		d := make([]byte, min(p.Filesz, 1<<20))
		n, _ := p.ReadAt(d, 0)
		if id := findBuildID(d[:n], f.ByteOrder); id != "" {
			return id
		}
	}
	return ""
}

// memBuildID는 core 안에 덤프된 ELF 머리 한 쪽(coredump_filter 4번 비트, 기본 켬)에서 build-id를 찾는다.
func (c *coreFile) memBuildID(base uint64) string {
	page := c.readMem(base, 4096)
	if len(page) < 64 || !bytes.HasPrefix(page, []byte(elf.ELFMAG)) || elf.Class(page[elf.EI_CLASS]) != elf.ELFCLASS64 {
		return ""
	}
	bo, size := c.bo, uint64(len(page))
	phoff, phent, phnum := bo.Uint64(page[32:]), uint64(bo.Uint16(page[54:])), uint64(bo.Uint16(page[56:]))
	if phoff >= size || phent < 56 {
		return ""
	}
	for i := uint64(0); i < phnum; i++ {
		o := phoff + i*phent // phoff < 4096, phent·phnum < 2^16: 넘치지 않는다
		if o+56 > size {
			break
		}
		if elf.ProgType(bo.Uint32(page[o:])) != elf.PT_NOTE {
			continue
		}
		off, sz := bo.Uint64(page[o+8:]), bo.Uint64(page[o+32:])
		if off >= size {
			continue
		}
		if id := findBuildID(page[off:off+min(sz, size-off)], bo); id != "" {
			return id
		}
	}
	return ""
}
