package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// serveUI serves the embedded single-page app. Real files are served as-is
// (hashed /assets/ files may be cached for good); any other path without a
// file extension is a client-side route, so it gets index.html and deep
// links and reloads work. A missing file with an extension is a 404.
func (s *Server) serveUI(writer http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if name != "" {
		if info, err := fs.Stat(s.web, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			s.assets.ServeHTTP(writer, request)
			return
		}
		if path.Ext(name) != "" {
			http.NotFound(writer, request)
			return
		}
	}
	http.ServeFileFS(writer, request, s.web, "index.html")
}
