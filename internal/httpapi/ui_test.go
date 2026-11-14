package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	webassets "github.com/nicko170/dismist"
)

// TestServesV1AppFromEmbeddedStub serves whatever ui/dist holds at / and
// every client route: the committed stub in the go job, the real build
// where the UI is built first (release workflow, image, local builds).
func TestServesV1AppFromEmbeddedStub(t *testing.T) {
	handler, _, _ := apiFixture(t)
	embedded, err := fs.ReadFile(webassets.UIAssets, "ui/dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/", "/sites/a/aps", "/onboard/register?step=verify"} {
		response := request(t, handler, http.MethodGet, target, "")
		if response.Code != http.StatusOK || response.Body.String() != string(embedded) {
			t.Fatalf("%s: %d %q", target, response.Code, response.Body.String())
		}
		if got := response.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: Cache-Control %q", target, got)
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Fatalf("%s: Content-Type %q", target, got)
		}
	}
	if response := request(t, handler, http.MethodGet, "/_ui/.keep", ""); response.Code != http.StatusNotFound {
		t.Fatalf("stub keep file served: %d", response.Code)
	}
}

func TestServeUIChoosesTheAppByPathPrefix(t *testing.T) {
	handler, _, _ := apiFixture(t)
	server := handler.(*Server)
	server.UseNextUI(fstest.MapFS{
		"index.html":                   {Data: []byte(`<!doctype html><div id="app">NEXT</div>`)},
		"_ui/index-AbC_e12x.js":        {Data: []byte(`console.log("next")`)},
		"_ui/favicon.svg":              {Data: []byte(`<svg/>`)},
		"_ui/icons/icon-192.png":       {Data: []byte{0x89, 'P', 'N', 'G'}},
		"_ui/manifest.webmanifest":     {Data: []byte(`{}`)},
		"_ui/route-sites-Zz09_-aB.css": {Data: []byte(`a{}`)},
	})
	cases := []struct {
		target       string
		status       int
		contains     string
		cacheControl string
		location     string
	}{
		// The v1 app is the default from M1, its assets under /_ui.
		{"/", 200, "NEXT", "no-cache", ""},
		{"/sites/a/overview", 200, "NEXT", "no-cache", ""},
		{"/system/backups?x=1", 200, "NEXT", "no-cache", ""},
		{"/nextish", 200, "NEXT", "no-cache", ""},
		{"/_uifoo", 200, "NEXT", "no-cache", ""},
		{"/_ui/index-AbC_e12x.js", 200, `console.log("next")`, immutableCC, ""},
		{"/_ui/route-sites-Zz09_-aB.css", 200, "a{}", immutableCC, ""},
		{"/_ui/favicon.svg", 200, "<svg/>", "no-cache", ""},
		{"/_ui/icons/icon-192.png", 200, "PNG", "no-cache", ""},
		{"/_ui/manifest.webmanifest", 200, "{}", "no-cache", ""},
		{"/_ui/missing-AbCdEf12.js", 404, `"code":"not_found"`, "", ""},
		{"/_ui/", 404, `"code":"not_found"`, "", ""},
		{"/_ui/icons", 404, `"code":"not_found"`, "", ""},
		// /next links from phases 0 and 1 redirect, keeping the query.
		{"/next", 301, "", "", "/"},
		{"/next/", 301, "", "", "/"},
		{"/next/sites/a/overview", 301, "", "", "/sites/a/overview"},
		{"/next/system/backups?x=1", 301, "", "", "/system/backups?x=1"},
		// The legacy app lives at /legacy/ until the release that deletes it.
		{"/legacy", 301, "", "", "/legacy/"},
		{"/legacy/", 200, `<div id="root"></div>`, "no-cache", ""},
		{"/legacy/anything", 200, `<div id="root"></div>`, "no-cache", ""},
		{"/missing.js", 404, `"code":"not_found"`, "", ""},
		{"/legacy/missing.js", 404, `"code":"not_found"`, "", ""},
		{"/auth/whatever", 404, `"code":"not_found"`, "", ""},
	}
	for _, tc := range cases {
		response := request(t, handler, http.MethodGet, tc.target, "")
		if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %q", tc.target, response.Code, response.Body.String())
		}
		if tc.cacheControl != "" && response.Header().Get("Cache-Control") != tc.cacheControl {
			t.Fatalf("%s: Cache-Control %q, want %q", tc.target, response.Header().Get("Cache-Control"), tc.cacheControl)
		}
		if tc.location != "" && response.Header().Get("Location") != tc.location {
			t.Fatalf("%s: Location %q, want %q", tc.target, response.Header().Get("Location"), tc.location)
		}
	}
	// Legacy hashed assets are served for good, at /assets/ (the build with
	// base '/') and /legacy/assets/ (base '/legacy/').
	assets, err := fs.ReadDir(server.web, "assets")
	if err != nil || len(assets) == 0 {
		t.Fatalf("legacy assets: %v", err)
	}
	for _, prefix := range []string{"/assets/", "/legacy/assets/"} {
		legacy := request(t, handler, http.MethodGet, prefix+assets[0].Name(), "")
		if legacy.Code != http.StatusOK || legacy.Header().Get("Cache-Control") != immutableCC {
			t.Fatalf("legacy asset %s: %d %q", prefix, legacy.Code, legacy.Header().Get("Cache-Control"))
		}
	}
	// HEAD on a client route answers without a body.
	head := request(t, handler, http.MethodHead, "/sites", "")
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatalf("HEAD /sites: %d %d bytes", head.Code, head.Body.Len())
	}
	// With the legacy app switched off, /legacy/ is just a client route.
	server.UseLegacyUI(false)
	if response := request(t, handler, http.MethodGet, "/legacy/", ""); !strings.Contains(response.Body.String(), "NEXT") {
		t.Fatalf("legacy off: %d %q", response.Code, response.Body.String())
	}
}

func TestNextAssetsServePrecompressedSiblings(t *testing.T) {
	handler, _, _ := apiFixture(t)
	server := handler.(*Server)
	server.UseNextUI(fstest.MapFS{
		"index.html":                {Data: []byte(`<!doctype html>`)},
		"_ui/index-AbC_e12x.js":     {Data: []byte(`console.log("plain")`)},
		"_ui/index-AbC_e12x.js.br":  {Data: []byte("BROTLI")},
		"_ui/index-AbC_e12x.js.gz":  {Data: []byte("GZIP")},
		"_ui/style-Zz09_-aB.css":    {Data: []byte(`a{}`)},
		"_ui/style-Zz09_-aB.css.gz": {Data: []byte("GZCSS")},
		"_ui/favicon.svg":           {Data: []byte(`<svg/>`)},
	})
	cases := []struct {
		target, accept, body, encoding, contentType string
		vary                                        bool
	}{
		{"/_ui/index-AbC_e12x.js", "gzip, deflate, br", "BROTLI", "br", "text/javascript", true},
		{"/_ui/index-AbC_e12x.js", "gzip", "GZIP", "gzip", "text/javascript", true},
		{"/_ui/index-AbC_e12x.js", "br;q=0, gzip;q=0.5", "GZIP", "gzip", "text/javascript", true},
		{"/_ui/index-AbC_e12x.js", "", `console.log("plain")`, "", "text/javascript", true},
		{"/_ui/style-Zz09_-aB.css", "br, gzip", "GZCSS", "gzip", "text/css", true},
		{"/_ui/style-Zz09_-aB.css", "br", "a{}", "", "text/css", true},
		{"/_ui/favicon.svg", "br, gzip", "<svg/>", "", "image/svg+xml", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		req.Host = "127.0.0.1:8088"
		if tc.accept != "" {
			req.Header.Set("Accept-Encoding", tc.accept)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusOK || response.Body.String() != tc.body {
			t.Fatalf("%s (%s): %d %q", tc.target, tc.accept, response.Code, response.Body.String())
		}
		if got := response.Header().Get("Content-Encoding"); got != tc.encoding {
			t.Fatalf("%s (%s): Content-Encoding %q, want %q", tc.target, tc.accept, got, tc.encoding)
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
			t.Fatalf("%s (%s): Content-Type %q, want %q", tc.target, tc.accept, got, tc.contentType)
		}
		if got := strings.Join(response.Header().Values("Vary"), ",") == "Accept-Encoding"; got != tc.vary {
			t.Fatalf("%s (%s): Vary %q", tc.target, tc.accept, response.Header().Get("Vary"))
		}
		if strings.HasPrefix(tc.target, "/_ui/index-") && response.Header().Get("Cache-Control") != immutableCC {
			t.Fatalf("%s: Cache-Control %q", tc.target, response.Header().Get("Cache-Control"))
		}
	}
}
