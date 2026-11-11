package model

import "testing"

func TestMaskSecrets(t *testing.T) {
	cases := []struct{ in, want string }{
		{"svc --rate /x/rate --push 127.0.0.1:9101", "svc --rate /x/rate --push 127.0.0.1:9101"},
		{"svc --password=hunter2 --port 1", "svc --password=*** --port 1"},
		{"svc --token abc.def -v", "svc --token *** -v"},
		{"Authorization: Bearer eyJhbGciOi", "Authorization: Bearer ***"},
		{`{"api_key": "k-123", "n": 1}`, `{"api_key": ***, "n": 1}`},
		{"DB_PASSWD=x1&user=a", "DB_PASSWD=***&user=a"},
		{"(E) GAP seq=1200 expected=1100", "(E) GAP seq=1200 expected=1100"},
	}
	for _, c := range cases {
		if got := MaskSecrets(c.in); got != c.want {
			t.Errorf("MaskSecrets(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSignalName(t *testing.T) {
	if SignalName(11) != "SIGSEGV" || SignalName(9) != "SIGKILL" || SignalName(40) != "SIG40" {
		t.Fatal(SignalName(11), SignalName(9), SignalName(40))
	}
}
