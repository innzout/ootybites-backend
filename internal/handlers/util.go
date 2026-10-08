package handlers

import (
	"encoding/json"
	"io"
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

// decodeJSONOptional decodes a JSON body when present but tolerates an empty or
// absent body (leaving dst at its zero value). For endpoints where the body is
// optional (e.g. an optional cancel reason).
func decodeJSONOptional(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && err != io.EOF {
		return err
	}
	return nil
}
