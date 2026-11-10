package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"eum/internal/manager/webauth"
)

// cmdPasswd는 표준 입력 첫 줄을 비밀번호로 읽어 web_auth.users[].hash에 넣을 해시를 찍는다(보안 S1).
// 비밀번호가 명령행·셸 기록에 남지 않도록 인자로 받지 않는다: printf '%s' 'pw' | eum passwd
func cmdPasswd(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		io.WriteString(stderr, "사용법: printf '%s' '<비밀번호>' | eum passwd   (표준 입력 첫 줄을 읽는다)\n")
		return errUsage
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("표준 입력: %w", err)
	}
	pw := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	h, err := webauth.HashPassword(pw)
	if err != nil {
		return fmt.Errorf("표준 입력 첫 줄이 비어 있습니다: %w", err)
	}
	fmt.Fprintln(stdout, h)
	return nil
}
