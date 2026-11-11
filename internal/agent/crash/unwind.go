package crash

import (
	"debug/elf"
	"fmt"

	"eum/internal/model"
)

// scanBytes는 내장 분석이 rsp부터 훑는 스택의 최대 길이다.
const scanBytes = 256 << 10

// builtinFrames는 gdb 없이 만드는 호출 스택이다(x86-64).
// 0번 칸은 죽은 곳(rip). 그 뒤는 rsp부터 스택의 8바이트 값을 훑어, 실행 가능한 매핑을 가리키고
// 바로 앞 기계어가 call인 값(돌아갈 주소)을 차례로 고른다 → Guess. 프레임 포인터 없는 -O2 빌드에서도 되지만
// 꼬리 호출로 빠진 칸은 없고(gdb도 같음), 이미 돌아온 호출이 남긴 값이 섞일 수 있다.
func builtinFrames(c *coreFile, sy *symbolizer, t *thread, limit int) ([]model.Frame, []string) {
	if c.machine != elf.EM_X86_64 {
		return nil, []string{fmt.Sprintf("내장 분석은 x86-64만 함(이 core는 %s): 호출 스택 없음", c.machine)}
	}
	if t == nil || len(t.regs) < numRegs {
		return nil, []string{"core에 레지스터(NT_PRSTATUS)가 없음: 호출 스택 없음"}
	}
	rip, rsp := t.regs[regRIP], t.regs[regRSP]
	frames := []model.Frame{sy.frame(rip, false)}
	stack := c.readMem(rsp, scanBytes)
	if len(stack) < 8 {
		return frames, []string{"스택 메모리가 core에 없음: 죽은 곳만 앎"}
	}
	last := rip
	for i := 0; i+8 <= len(stack) && len(frames) < limit; i += 8 {
		w := c.bo.Uint64(stack[i:])
		if w == last || w < 8 || !c.executable(w) || sy.moduleOf(w) == nil {
			continue
		}
		if !afterCall(sy.code(w-7, 7)) {
			continue
		}
		f := sy.frame(w, true)
		f.Guess = true
		frames = append(frames, f)
		last = w
	}
	return frames, nil
}

// afterCall은 b(돌아갈 주소 바로 앞 7바이트)가 call 명령으로 끝나는가다:
// e8 rel32(5바이트), 또는 ff /2(call r/m64, REX 접두가 붙을 수 있음, 2~7바이트).
func afterCall(b []byte) bool {
	if len(b) != 7 {
		return false
	}
	if b[2] == 0xe8 {
		return true
	}
	for l := 2; l <= 7; l++ {
		i := 7 - l
		if b[i] == 0xff && callModRMLen(b[i+1:]) == l-1 {
			return true
		}
		if l >= 3 && b[i]&0xf0 == 0x40 && b[i+1] == 0xff && callModRMLen(b[i+2:]) == l-2 {
			return true
		}
	}
	return false
}

// callModRMLen은 ff /2의 ModRM부터의 길이(ModRM + SIB + 변위)다. /2가 아니면 0.
func callModRMLen(b []byte) int {
	if len(b) == 0 || b[0]&0x38 != 0x10 {
		return 0
	}
	mod, rm := b[0]>>6, b[0]&7
	n := 1
	if mod != 3 && rm == 4 { // SIB
		if len(b) < 2 {
			return 0
		}
		n++
		if mod == 0 && b[1]&7 == 5 {
			n += 4
		}
	}
	switch {
	case mod == 0 && rm == 5:
		n += 4 // rip 기준
	case mod == 1:
		n++
	case mod == 2:
		n += 4
	}
	return n
}
