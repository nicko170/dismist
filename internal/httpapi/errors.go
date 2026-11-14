package httpapi

import (
	"errors"
	"net/http"
)

var errNotFound = errors.New("network not found")
var errConflict = errors.New("AP already registered")
var errNoChange = errors.New("no AP changes")

func writeChangeError(writer http.ResponseWriter, err error) {
	if errors.Is(err, errNotFound) {
		http.Error(writer, err.Error(), http.StatusNotFound)
		return
	}
	if errors.Is(err, errConflict) {
		http.Error(writer, err.Error(), http.StatusConflict)
		return
	}
	http.Error(writer, err.Error(), http.StatusBadRequest)
}
