package coderepo

import (
	"fmt"
	"strings"
	"testing"
)

// fnsOf는 "이름 start-end" 목록이다(sig가 start와 다르면 "이름 start/sig-end").
func fnsOf(rel, src string) []string {
	var out []string
	for _, f := range parseFuncs(rel, []byte(src)) {
		if f.sig != f.start {
			out = append(out, fmt.Sprintf("%s %d/%d-%d", f.name, f.start, f.sig, f.end))
		} else {
			out = append(out, fmt.Sprintf("%s %d-%d", f.name, f.start, f.end))
		}
	}
	return out
}

func eqList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		t.Errorf("%s\n got: %q\nwant: %q", what, got, want)
	}
}

func TestParseC(t *testing.T) {
	src := `#include <stdio.h>
#define NOINL __attribute__((noinline))
#define OPEN {
#define LONG(x) do { \
    x; \
} while (0)

typedef struct { int a; } pair_t;          /* 줄 8 */
static int table[] = { 1, 2, 3 };
static void logf_(const char *fmt, ...) __attribute__((format(printf, 1, 2)));
static int proto(int a);

/* 짝 안 맞는 괄호가 문자열·문자·주석에:
 * "{" '}' { */
static NOINL int book_insert(const pair_t *o) {
    const char *s = "}}} {";    // }
    char c = '}';
    if (o->a) { return 1; }
    return s[0] + c;
}

static void logf_(const char *fmt, ...)
{
    printf("%s\n", fmt);
}

int
multi_line(int a,
           int b)
{
    return a + b;
}

static inline __attribute__((always_inline)) unsigned burn(unsigned x) {
    return x * 3;
}

static int proto(int a) { return a; }
`
	eqList(t, "C", fnsOf("x.c", src), []string{
		"book_insert 13/15-20",
		"logf_ 22-25",
		"multi_line 27-32",
		"burn 34-36",
		"proto 38-38",
	})
}

func TestParseLeadCommentLimit(t *testing.T) {
	src := "// 1\n// 2\n// 3\n// 4\n// 5\n// 6\n// 7\nint f(void) {\n}\n\n// 붙은 주석\n\nint g(void) {\n}\n"
	eqList(t, "lead", fnsOf("x.c", src), []string{"f 3/8-9", "g 13-14"})
}

func TestParseCppJava(t *testing.T) {
	cpp := `namespace eum {
class Book {
public:
    Book(int n) : n_(n), m_(0) {}
    int size() const { return n_; }
private:
    int n_, m_;
};
int Book::insert(int x) noexcept {
    return x;
}
}  // namespace eum
extern "C" {
void c_entry(void) {
}
}
static void (*pick(int k))(int) { return 0; }
`
	eqList(t, "C++", fnsOf("b.cpp", cpp), []string{"Book 4-4", "size 5-5", "insert 9-11", "c_entry 14-15"})

	java := `package x;

import java.util.List;

public class Svc {
    private static final int N = compute();
    private final Runnable r = () -> { run(); };

    static { init(); }

    /** 주문 처리 */
    @Override
    public void run() {
        if (x) { y(); }
    }

    @RequestMapping(value = "/a")
    public <T> List<T> handle(@Body Req req) throws Exception {
        return null;
    }

    enum Kind { A, B; int code() { return 1; } }
}
`
	eqList(t, "Java", fnsOf("Svc.java", java), []string{"run 11/12-15", "handle 17-20", "code 22-22"})
}

func TestParseGoJS(t *testing.T) {
	gosrc := "package x\n\nimport (\n\t\"fmt\"\n)\n\nvar v = struct{ a int }{1}\n\ntype T struct {\n\tA string `json:\"a{\"`\n}\n\n// M은 메서드다.\nfunc (t *T) M(a int) (int, error) {\n\treturn a, nil\n}\n\nfunc Map[E any](xs []E, f func(E) E) []E {\n\tfmt.Println(`}`)\n\treturn xs\n}\n"
	eqList(t, "Go", fnsOf("x.go", gosrc), []string{"M 13/14-16", "Map 18-21"})

	js := `import x from 'y'
const a = { b: 1 }
describe('x', () => {
  it('y', function () {})
})
export async function load(id) {
  return id
}
const save = async (v) => {
  return v
}
class Pane {
  render() {
    return 1
  }
  onClick = (e) => {
  }
}
function
later() {}
`
	eqList(t, "JS", fnsOf("p.js", js), []string{"load 6-8", "save 9-11", "render 13-15", "onClick 16-17", "later 19-20"})
}

func TestParseShellPython(t *testing.T) {
	sh := `#!/bin/sh
# 시작한다
start() {
  echo "}" '{'   # }
  if [ -n "${X}" ]; then :; fi
}
function stop {
  :
}
helper()
{
  :
}
`
	eqList(t, "sh", fnsOf("run.sh", sh), []string{"start 2/3-6", "stop 7-9", "helper 10-13"})
	eqList(t, "shebang", fnsOf("bin/run", sh), []string{"start 2/3-6", "stop 7-9", "helper 10-13"})

	py := `import os

# 읽는다
@cache
def read(path,
         mode="r"):
    s = """
def fake():
"""
    def inner():
        return 1
    return s

class A:
    async def go(self): pass

x = 1
`
	eqList(t, "py", fnsOf("m.py", py), []string{"read 3/4-12", "inner 10-11", "go 15-15"})
}

func TestNormSymbol(t *testing.T) {
	for in, want := range map[string]string{
		"book_insert":                  "book_insert",
		"book_insert.isra.0":           "book_insert",
		"book_insert.cold":             "book_insert",
		"match_order.constprop.0.isra": "match_order",
		"book_insert+0x1c":             "book_insert",
		"puts@plt":                     "puts",
		"eum::Book::insert(int) const": "insert",
		"main.(*Server).Handle":        "Handle",
		"com.x.Svc.run":                "run",
		"std::max<int>":                "max",
	} {
		if got := normSymbol(in); got != want {
			t.Errorf("normSymbol(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGlob(t *testing.T) {
	f, err := newFilter([]string{"demo/**", "src"}, []string{"demo/tmp/**"})
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]bool{
		"demo/a.c":         true,
		"demo/x/y.c":       true,
		"src/a/b.go":       true,
		"other/a.c":        false,
		"demo/tmp/a.c":     false,
		"demo/.env":        false,
		"demo/.env.local":  false,
		"demo/srv.PEM":     false,
		"demo/my_Secret.c": false,
		"demo/secrets/a.c": false,
		"demo/.git/config": false,
		"demo/id_rsa.pub":  false,
		"demo/x.key":       false,
	} {
		if got := f.fileOK(rel); got != want {
			t.Errorf("fileOK(%q) = %v, want %v", rel, got, want)
		}
	}
	for rel, want := range map[string]bool{"demo": true, "src": true, "src/a": true, "other": false, "demo/tmp": false, ".git": false} {
		if got := f.dirOK(rel); got != want {
			t.Errorf("dirOK(%q) = %v, want %v", rel, got, want)
		}
	}
	if _, err := newFilter([]string{"a/[x"}, nil); err == nil {
		t.Error("틀린 패턴을 받음")
	}
	if _, err := newFilter(nil, []string{"../x"}); err == nil {
		t.Error(".. 패턴을 받음")
	}
}
