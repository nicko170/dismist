package httpapi

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// The two web apps (design §2.4). The legacy app (web/dist) answers at /
// with its assets under /assets/. The v1 app (ui/dist) is one build with
// base '/' and every asset under /_ui/; its router picks the basepath at
// runtime, so in phases 0 and 1 it is served at /next/. serveUI picks the
// app by path prefix, never by guessing from an asset name.
const (
	nextPrefix  = "/next"
	nextAssets  = "/_ui"
	immutableCC = "public, max-age=31536000, immutable"
)

// hashedName matches Vite's content-hashed file names (name-HASH.ext).
var hashedName = regexp.MustCompile(`-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)

// UseNextUI sets the v1 app's files (the contents of ui/dist). New uses the
// embedded build.
func (s *Server) UseNextUI(files fs.FS) *Server {
	s.next = files
	return s
}

// serveUI serves the embedded single-page apps for GET and HEAD:
//   - /_ui/* are the v1 app's files: hashed ones may be cached for good,
//     the rest (icons, manifest) are revalidated;
//   - /next and /next/* are the v1 app's client-side routes: its
//     index.html, revalidated on every load (Cache-Control: no-cache);
//   - everything else is the legacy app as before: real files as-is
//     (hashed /assets/ files cached for good), and any path without a file
//     extension gets its index.html so deep links and reloads work.
//
// A missing file with an extension is a 404.
func (s *Server) serveUI(writer http.ResponseWriter, request *http.Request) {
	clean := path.Clean("/" + request.URL.Path)
	switch {
	case clean == nextAssets || strings.HasPrefix(clean, nextAssets+"/"):
		s.serveNextAsset(writer, request, strings.TrimPrefix(clean, "/"))
	case clean == nextPrefix || strings.HasPrefix(clean, nextPrefix+"/"):
		if path.Ext(clean) != "" {
			routeNotFound(writer)
			return
		}
		s.serveNextIndex(writer, request)
	default:
		s.serveLegacy(writer, request, strings.TrimPrefix(clean, "/"))
	}
}

func (s *Server) serveNextAsset(writer http.ResponseWriter, request *http.Request, name string) {
	if s.next == nil {
		routeNotFound(writer)
		return
	}
	info, err := fs.Stat(s.next, name)
	if err != nil || info.IsDir() || strings.HasPrefix(path.Base(name), ".") {
		routeNotFound(writer)
		return
	}
	if hashedName.MatchString(name) {
		writer.Header().Set("Cache-Control", immutableCC)
	} else {
		writer.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(writer, request, s.next, s.precompressed(writer, request, name))
}

// precompressedEncodings are the sibling files the v1 build writes next to
// its assets (name.br, name.gz), in order of preference.
var precompressedEncodings = []struct{ token, suffix string }{{"br", ".br"}, {"gzip", ".gz"}}

// precompressed returns the file to serve for name: its .br or .gz sibling
// when the build has one and the client accepts that encoding (with
// Content-Encoding and the original Content-Type set), else name itself.
// Vary: Accept-Encoding is set whenever a sibling exists, so a cache never
// hands the compressed bytes to a client that did not ask for them.
func (s *Server) precompressed(writer http.ResponseWriter, request *http.Request, name string) string {
	accepted := acceptedEncodings(request.Header.Get("Accept-Encoding"))
	varied := false
	for _, encoding := range precompressedEncodings {
		info, err := fs.Stat(s.next, name+encoding.suffix)
		if err != nil || info.IsDir() {
			continue
		}
		if !varied {
			writer.Header().Add("Vary", "Accept-Encoding")
			varied = true
		}
		if !accepted[encoding.token] {
			continue
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			return name
		}
		writer.Header().Set("Content-Type", contentType)
		writer.Header().Set("Content-Encoding", encoding.token)
		return name + encoding.suffix
	}
	return name
}

// acceptedEncodings parses Accept-Encoding into the codings with a non-zero
// quality.
func acceptedEncodings(header string) map[string]bool {
	result := map[string]bool{}
	for _, part := range strings.Split(header, ",") {
		token, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		token = strings.ToLower(strings.TrimSpace(token))
		if token == "" {
			continue
		}
		accepted := true
		for _, param := range strings.Split(params, ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				if quality, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && quality == 0 {
					accepted = false
				}
			}
		}
		result[token] = accepted
	}
	return result
}

func (s *Server) serveNextIndex(writer http.ResponseWriter, request *http.Request) {
	if s.next == nil {
		routeNotFound(writer)
		return
	}
	raw, err := fs.ReadFile(s.next, "index.html")
	if err != nil {
		routeNotFound(writer)
		return
	}
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(raw)
	}
}

func (s *Server) serveLegacy(writer http.ResponseWriter, request *http.Request, name string) {
	if name != "" {
		if info, err := fs.Stat(s.web, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				writer.Header().Set("Cache-Control", immutableCC)
			}
			s.assets.ServeHTTP(writer, request)
			return
		}
		if path.Ext(name) != "" {
			routeNotFound(writer)
			return
		}
	}
	http.ServeFileFS(writer, request, s.web, "index.html")
}
