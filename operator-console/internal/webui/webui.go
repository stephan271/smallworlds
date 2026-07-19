package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var assets embed.FS

func New(api http.Handler) http.Handler {
	staticFiles, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	static := http.FileServer(http.FS(staticFiles))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/api/") {
			api.ServeHTTP(response, request)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		static.ServeHTTP(response, request)
	})
}
