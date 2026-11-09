package exec

import "bytes"

// Marker는 실행이 자식 환경에 심은 표식(05 §1)이다.
type Marker struct {
	ExecID, Flow, Step, Host string
	Profile                  bool
}

// ParseMarker는 /proc/[pid]/environ(NUL로 나뉜 KEY=VALUE)에서 EUM_* 표식을 읽는다.
// EUM_EXEC_ID가 없으면 false를 돌려준다.
//
// 예:
//
//	m, ok := ParseMarker([]byte("PATH=/bin\x00EUM_EXEC_ID=01H\x00EUM_PROFILE=1\x00")) // ok, m.Profile
func ParseMarker(environ []byte) (Marker, bool) {
	var m Marker
	for _, kv := range bytes.Split(environ, []byte{0}) {
		k, v, ok := bytes.Cut(kv, []byte{'='})
		if !ok {
			continue
		}
		switch string(k) {
		case "EUM_EXEC_ID":
			m.ExecID = string(v)
		case "EUM_FLOW":
			m.Flow = string(v)
		case "EUM_STEP":
			m.Step = string(v)
		case "EUM_HOST":
			m.Host = string(v)
		case "EUM_PROFILE":
			m.Profile = string(v) == "1"
		}
	}
	if m.ExecID == "" {
		return Marker{}, false
	}
	return m, true
}
