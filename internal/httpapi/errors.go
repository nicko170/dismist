package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/nicko170/dismist/internal/api"
	"github.com/nicko170/dismist/internal/compile"
)

// errNoChange ends a mutation that would change nothing; the handler then
// answers with the stored object.
var errNoChange = errors.New("no AP changes")

// apiError is an error response in the {error, code, fields} contract
// (design §4.8). Handlers return it from store mutations or write it
// directly with writeError.
type apiError struct {
	status  int
	code    api.ErrorCode
	message string
	fields  map[string]string
}

func (e *apiError) Error() string { return e.message }

func newError(status int, code api.ErrorCode, message string) *apiError {
	return &apiError{status: status, code: code, message: message}
}

// withField adds a field error (a JSON path of the request body).
func (e *apiError) withField(path, message string) *apiError {
	if e.fields == nil {
		e.fields = map[string]string{}
	}
	e.fields[path] = message
	return e
}

// invalid is a 400 for a malformed request.
func invalid(message string) *apiError {
	return newError(http.StatusBadRequest, api.ErrorCodeInvalid, message)
}

// invalidField is a 400 naming one request field. The message is used for
// both the response and the field, so clients that show only `error` still
// say what is wrong.
func invalidField(path, message string) *apiError {
	return invalid(message).withField(path, message)
}

// notFound is a 404 for a missing object, named in the message ("site",
// "network", "AP", …).
func notFound(what string) *apiError {
	return newError(http.StatusNotFound, api.ErrorCodeNotFound, what+" not found")
}

// conflict is a 409.
func conflict(message string) *apiError {
	return newError(http.StatusConflict, api.ErrorCodeConflict, message)
}

// internalError is a 500. The message never carries an internal error
// string; log those instead.
func internalError(message string) *apiError {
	return newError(http.StatusInternalServerError, api.ErrorCodeInternal, message)
}

func forbidden(message string) *apiError {
	return newError(http.StatusForbidden, api.ErrorCodeForbidden, message)
}

// writeAPIError writes e as JSON.
func writeAPIError(writer http.ResponseWriter, e *apiError) {
	fields := e.fields
	if fields == nil {
		fields = map[string]string{}
	}
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	respond(writer, e.status, api.ErrorBody{Error: e.message, Code: e.code, Fields: fields})
}

// writeError writes any error from a handler or a store mutation in the
// error contract. Errors that are not *apiError keep the status the API
// always used for them: 400 with the error text (store validation), and a
// compile failure is a 400 that names the AP.
func writeError(writer http.ResponseWriter, err error) {
	var typed *apiError
	if errors.As(err, &typed) {
		writeAPIError(writer, typed)
		return
	}
	var compileErr *compile.APError
	if errors.As(err, &compileErr) {
		writeAPIError(writer, invalid(fmt.Sprintf("the change would make AP %s's config invalid: %v", compileErr.APID, compileErr.Err)))
		return
	}
	writeAPIError(writer, invalid(err.Error()))
}

// writeChangeError writes the error of a store mutation.
func writeChangeError(writer http.ResponseWriter, err error) { writeError(writer, err) }

// routeNotFound is the 404 for a path no handler serves.
func routeNotFound(writer http.ResponseWriter) {
	writeAPIError(writer, newError(http.StatusNotFound, api.ErrorCodeNotFound, "no such endpoint"))
}

// decodeJSON reads one JSON object from the request body into value, with
// unknown fields refused and the body capped at limit bytes. It requires
// Content-Type application/json. Its errors are *apiError values naming the
// field at fault where it can.
func decodeJSON(request *http.Request, limit int64, value any, what string) error {
	if !isJSON(request) {
		return invalid("application/json required")
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return decodeError(err, what)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalid("one JSON object required")
	}
	return nil
}

// isJSON accepts Content-Type application/json with or without parameters
// (for example "; charset=utf-8").
func isJSON(request *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

// decodeError turns a json decoding error into a 400, with a field error
// for an unknown field or a value of the wrong type.
func decodeError(err error, what string) *apiError {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return invalid("invalid "+what).withField(typeErr.Field, fmt.Sprintf("must be a JSON %s", jsonKind(typeErr.Type.Kind().String())))
	}
	if field, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		field = strings.Trim(field, `"`)
		return invalid("invalid "+what).withField(field, "unknown field")
	}
	return invalid("invalid " + what)
}

func jsonKind(kind string) string {
	switch kind {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "slice", "array":
		return "array"
	case "map", "struct":
		return "object"
	default:
		return "number"
	}
}
