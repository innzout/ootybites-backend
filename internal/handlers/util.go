package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/innzout/ootybites/pkg/response"
)

// decodeJSON reads a JSON request body into dst. On malformed JSON it writes a
// 400 envelope and returns false, so callers can `if !decodeJSON(...) { return }`.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		response.Fail(w, http.StatusBadRequest, response.CodeValidation, "Invalid request body")
		return false
	}
	return true
}
