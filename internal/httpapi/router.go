package httpapi

import (
	"net/http"
	"sort"
	"strings"
)

// params are the {name} segments of a matched route.
type params map[string]string

type handlerFunc func(http.ResponseWriter, *http.Request, params)

// route is one method and path pattern. Segments in braces match one path
// segment; nothing else is a wildcard.
type route struct {
	method  string
	pattern []string
	handler handlerFunc
}

// router is a small exact-segment router. Unlike http.ServeMux it answers
// every miss in the JSON error contract: an unknown path is a 404 and a
// known path with another method a 405 with Allow (design §4.8).
type router struct{ routes []route }

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// handle registers a route. Routes with more literal segments are tried
// first, so /api/aps/pending beats /api/aps/{id}.
func (r *router) handle(method, pattern string, handler handlerFunc) {
	r.routes = append(r.routes, route{method: method, pattern: splitPath(pattern), handler: handler})
	sort.SliceStable(r.routes, func(i, j int) bool { return literals(r.routes[i].pattern) > literals(r.routes[j].pattern) })
}

func (r *router) match(segments []string, pattern []string) (params, bool) {
	if len(segments) != len(pattern) {
		return nil, false
	}
	var values params
	for index, part := range pattern {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if segments[index] == "" {
				return nil, false
			}
			if values == nil {
				values = params{}
			}
			values[part[1:len(part)-1]] = segments[index]
			continue
		}
		if part != segments[index] {
			return nil, false
		}
	}
	if values == nil {
		values = params{}
	}
	return values, true
}

// serve dispatches the request and reports whether a route matched the
// path (with any method). HEAD is served by GET routes.
func (r *router) serve(writer http.ResponseWriter, request *http.Request) bool {
	segments := splitPath(request.URL.Path)
	method := request.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	var allowed []string
	for _, candidate := range r.routes {
		values, ok := r.match(segments, candidate.pattern)
		if !ok {
			continue
		}
		if candidate.method != method {
			allowed = append(allowed, candidate.method)
			continue
		}
		candidate.handler(writer, request, values)
		return true
	}
	if len(allowed) == 0 {
		return false
	}
	sort.Strings(allowed)
	writer.Header().Set("Allow", strings.Join(dedupe(allowed), ", "))
	writeAPIError(writer, newError(http.StatusMethodNotAllowed, "invalid", request.Method+" is not allowed here"))
	return true
}

func literals(pattern []string) int {
	count := 0
	for _, part := range pattern {
		if !strings.HasPrefix(part, "{") {
			count++
		}
	}
	return count
}

func dedupe(values []string) []string {
	result := values[:0]
	for index, value := range values {
		if index == 0 || values[index-1] != value {
			result = append(result, value)
		}
	}
	return result
}
