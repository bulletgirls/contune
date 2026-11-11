package crash

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// 손으로 만드는 최소 ELF64(x86-64, little endian) core. 테스트 전용.

type testNote struct {
	name string
	typ  uint32
	desc []byte
}

type testLoad struct {
	vaddr  uint64
	flags  uint32 // PF_X=1, PF_W=2, PF_R=4
	data   []byte // 파일에 들어가는 바이트
	memsz  uint64 // 0이면 len(data)
	filesz uint64 // 0이면 len(data). 파일보다 크게 주면 잘린 core
}

var le = binary.LittleEndian

func encodeNotes(notes []testNote) []byte {
	var b []byte
	pad := func() {
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	for _, n := range notes {
		name := append([]byte(n.name), 0)
		b = le.AppendUint32(b, uint32(len(name)))
		b = le.AppendUint32(b, uint32(len(n.desc)))
		b = le.AppendUint32(b, n.typ)
		b = append(b, name...)
		pad()
		b = append(b, n.desc...)
		pad()
	}
	return b
}

// buildCore는 PT_NOTE 하나와 PT_LOAD들로 된 core 바이트를 만든다.
func buildCore(notes []testNote, loads []testLoad) []byte {
	const ehsize, phentsize = 64, 56
	nph := 1 + len(loads)
	noteData := encodeNotes(notes)
	off := uint64(ehsize + phentsize*nph)
	b := make([]byte, ehsize)
	copy(b, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	le.PutUint16(b[16:], 4)  // ET_CORE
	le.PutUint16(b[18:], 62) // EM_X86_64
	le.PutUint32(b[20:], 1)
	le.PutUint64(b[32:], ehsize)
	le.PutUint16(b[52:], ehsize)
	le.PutUint16(b[54:], phentsize)
	le.PutUint16(b[56:], uint16(nph))
	le.PutUint16(b[58:], 64)

	ph := func(typ, flags uint32, o, vaddr, filesz, memsz, align uint64) {
		b = le.AppendUint32(b, typ)
		b = le.AppendUint32(b, flags)
		b = le.AppendUint64(b, o)
		b = le.AppendUint64(b, vaddr)
		b = le.AppendUint64(b, 0)
		b = le.AppendUint64(b, filesz)
		b = le.AppendUint64(b, memsz)
		b = le.AppendUint64(b, align)
	}
	ph(4, 0, off, 0, uint64(len(noteData)), 0, 4) // PT_NOTE
	cur := off + uint64(len(noteData))
	for _, l := range loads {
		fs := l.filesz
		if fs == 0 {
			fs = uint64(len(l.data))
		}
		ms := l.memsz
		if ms == 0 {
			ms = fs
		}
		ph(1, l.flags, cur, l.vaddr, fs, ms, 0x1000)
		cur += uint64(len(l.data))
	}
	b = append(b, noteData...)
	for _, l := range loads {
		b = append(b, l.data...)
	}
	return b
}

// prstatusDesc는 x86-64 elf_prstatus(336바이트)다.
func prstatusDesc(cursig, pid int, rip, rsp uint64) []byte {
	d := make([]byte, 336)
	le.PutUint16(d[12:], uint16(cursig))
	le.PutUint32(d[32:], uint32(pid))
	le.PutUint64(d[112+16*8:], rip)
	le.PutUint64(d[112+19*8:], rsp)
	return d
}

// siginfoDesc는 64비트 siginfo(128바이트)다. code > 0이면 addr를, 아니면 보낸 pid·uid를 담는다.
func siginfoDesc(signo, code int, addr uint64, pid, uid int) []byte {
	d := make([]byte, 128)
	le.PutUint32(d[0:], uint32(int32(signo)))
	le.PutUint32(d[8:], uint32(int32(code)))
	if code > 0 && code != 0x80 {
		le.PutUint64(d[16:], addr)
	} else {
		le.PutUint32(d[16:], uint32(pid))
		le.PutUint32(d[20:], uint32(uid))
	}
	return d
}

type testMap struct {
	start, end, pgoff uint64
	name              string
}

func fileDesc(maps []testMap) []byte {
	var d []byte
	d = le.AppendUint64(d, uint64(len(maps)))
	d = le.AppendUint64(d, 4096)
	for _, m := range maps {
		d = le.AppendUint64(d, m.start)
		d = le.AppendUint64(d, m.end)
		d = le.AppendUint64(d, m.pgoff)
	}
	for _, m := range maps {
		d = append(d, m.name...)
		d = append(d, 0)
	}
	return d
}

func prpsinfoDesc(pid, uid, gid int, fname, args string) []byte {
	d := make([]byte, 136)
	le.PutUint32(d[16:], uint32(uid))
	le.PutUint32(d[20:], uint32(gid))
	le.PutUint32(d[24:], uint32(pid))
	copy(d[40:56], fname)
	copy(d[56:136], args)
	return d
}

func writeFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// sampleCore는 matching_svc(pid 4242)가 0x8을 건드려 SEGV_MAPERR로 죽은 core다. 스레드 2개.
func sampleCore(sig []byte) []byte {
	const exeBase = 0x555555554000
	stack := make([]byte, 64)
	return buildCore([]testNote{
		{"CORE", ntPRSTATUS, prstatusDesc(11, 4242, exeBase+0x1139, 0x7ffc00000000)},
		{"CORE", ntPRPSINFO, prpsinfoDesc(4242, 1000, 1001, "matching_svc", "matching_svc --port 9000")},
		{"CORE", ntSIGINFO, sig},
		{"CORE", ntFILE, fileDesc([]testMap{
			{exeBase, exeBase + 0x1000, 0, "/nonexistent/bin/matching_svc"},
			{exeBase + 0x1000, exeBase + 0x2000, 1, "/nonexistent/bin/matching_svc"},
			{0x7f0000000000, 0x7f0000028000, 0, "/nonexistent/lib/libc.so.6"},
		})},
		{"CORE", ntPRSTATUS, prstatusDesc(11, 4243, 0x7f0000001000, 0x7ffd00000000)},
		{"LINUX", 0x202, make([]byte, 32)}, // NT_X86_XSTATE 같은 남의 note는 건너뛴다
	}, []testLoad{
		{vaddr: exeBase + 0x1000, flags: 5, data: nil, memsz: 0x1000, filesz: 0},
		{vaddr: 0x7ffc00000000, flags: 6, data: stack},
	})
}
