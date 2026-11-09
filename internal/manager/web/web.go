// Package web은 바이너리에 내장한 화면 정적 파일이다(외부 CDN 없음).
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:static
var static embed.FS

// Mount는 GET /에 정적 파일을 붙인다.
func Mount(mux *http.ServeMux) {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(sub)))
}
