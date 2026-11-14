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

// The web apps (design §2.4, from M1). The v1 app (ui/dist) is the
// default: every client-side route gets its index.html and its assets live
// under /_ui/. /next and /next/* redirect (301) to the same path without
// the prefix, so shared phase-0 links keep working. The legacy app
// (web/dist) is served at /legacy/ until the release that deletes it; its
// build references /assets/ (base '/') or /legacy/assets/ (base
// '/legacy/'), and both are served. serveUI picks the app by path prefix,
// never by guessing from an asset name.
const (
	nextPrefix   = "/next"
	nextAssets   = "/_ui"
	legacyPrefix = "/legacy"
	immutableCC  = "public, max-age=31536000, immutable"
)

// hashedName matches Vite's content-hashed file names (name-HASH.ext).
var hashedName = regexp.MustCompile(`-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)

// UseNextUI sets the v1 app's files (the contents of ui/dist). New uses the
// embedded build.
func (s *Server) UseNextUI(files fs.FS) *Server {
	s.next = files
	return s
}

// serveUI serves the embedded single-page apps for GET and HEAD. A missing
// file with an extension is a 404.
func (s *Server) serveUI(writer http.ResponseWriter, request *http.Request) {
	clean := path.Clean("/" + request.URL.Path)
	// The legacy app edits the legacy advanced patches; once the schema-2
	// migration commits they are no longer what the APs compile from, so
	// it is not served at all (design §8.5 D).
	legacy := s.legacyUI && !s.typedEnabled()
	switch {
	case clean == nextAssets || strings.HasPrefix(clean, nextAssets+"/"):
		s.serveNextAsset(writer, request, strings.TrimPrefix(clean, "/"))
	case clean == nextPrefix || strings.HasPrefix(clean, nextPrefix+"/"):
		target := strings.TrimPrefix(clean, nextPrefix)
		if target == "" {
			target = "/"
		}
		if request.URL.RawQuery != "" {
			target += "?" + request.URL.RawQuery
		}
		http.Redirect(writer, request, target, http.StatusMovedPermanently)
	case legacy && clean == legacyPrefix && !strings.HasSuffix(request.URL.Path, "/"):
		http.Redirect(writer, request, legacyPrefix+"/", http.StatusMovedPermanently)
	case legacy && (clean == legacyPrefix || strings.HasPrefix(clean, legacyPrefix+"/")):
		s.serveLegacy(writer, request, strings.TrimPrefix(strings.TrimPrefix(clean, legacyPrefix), "/"))
	case legacy && strings.HasPrefix(clean, "/assets/"):
		s.serveLegacy(writer, request, strings.TrimPrefix(clean, "/"))
	default:
		if path.Ext(clean) != "" {
			routeNotFound(writer)
			return
		}
		s.serveNextIndex(writer, request)
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
			http.ServeFileFS(writer, request, s.web, name)
			return
		}
		if path.Ext(name) != "" {
			routeNotFound(writer)
			return
		}
	}
	raw, err := fs.ReadFile(s.web, "index.html")
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
