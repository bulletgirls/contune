package model

// 보드 검증(카드 W-A가 채운다). 규칙은 docs/cards/W-boards.md W-A 절.

// DashIDOK는 보드 ID 규칙이다: 소문자·숫자로 시작하고 소문자·숫자·_-만 64자까지, "templates"·"metrics"·"data"는 쓸 수 없다.
func DashIDOK(id string) bool {
	return id != "" // W-A
}

// ValidateDash는 보드 정의를 검사한다. 사람이 읽을 첫 오류 하나를 돌려준다(위젯이면 "위젯 <id>: …").
func ValidateDash(d Dash) error {
	return nil // W-A
}
