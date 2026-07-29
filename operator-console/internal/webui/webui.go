package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var assets embed.FS

// New serves the embedded client with an API handler mounted under /api/. It is
// used by the Bootstrap Launcher, whose landing screen is the client's root.
func New(api http.Handler) http.Handler {
	return handler(api, "")
}

// NewConsole serves the same embedded client for the in-cluster Operator
// Console, whose landing screen is the console route rather than the launcher's
// setup journey. A request for the root is redirected there, so an Operator who
// opens the console hostname arrives at the console and not at a setup wizard
// for a cluster that already exists.
func NewConsole(api http.Handler) http.Handler {
	return handler(api, "/console")
}

func handler(api http.Handler, landing string) http.Handler {
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
		if landing != "" && (request.URL.Path == "/" || request.URL.Path == "") {
			target := landing
			if request.URL.RawQuery != "" {
				// The OIDC callback redirects back to the root carrying an
				// auth_error; losing the query here would turn a reportable login
				// failure into a silent one.
				target += "?" + request.URL.RawQuery
			}
			http.Redirect(response, request, target, http.StatusFound)
			return
		}
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		request.URL.Path = resolvePage(staticFiles, request.URL.Path)
		static.ServeHTTP(response, request)
	})
}

// resolvePage maps a clean route path to the page the static build wrote for it.
// The adapter prerenders /console as console.html, which a plain file server
// would answer with 404 — the route has to be resolved to its page for the
// client's non-root screens to be reachable at all.
func resolvePage(files fs.FS, requestPath string) string {
	if requestPath == "" || strings.HasSuffix(requestPath, "/") || path.Ext(requestPath) != "" {
		return requestPath
	}
	candidate := strings.TrimPrefix(requestPath, "/") + ".html"
	if _, err := fs.Stat(files, candidate); err == nil {
		return "/" + candidate
	}
	return requestPath
}
