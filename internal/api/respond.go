package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/wiebe-xyz/bugbarn/internal/apperr"
)

// positiveIDParam reads a bare numeric id wildcard. A value that is not a
// positive integer is answered with 400 and reported as !ok. Prefixed ids
// ("alert-7") are parsed by the storage layer, which answers the same 400.
func positiveIDParam(w http.ResponseWriter, r *http.Request, name, kind string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid "+kind+" id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeServiceError(w http.ResponseWriter, err error) {
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case "not_found":
			http.Error(w, "not found", http.StatusNotFound)
		case "conflict":
			http.Error(w, "conflict", http.StatusConflict)
		case "invalid_input":
			http.Error(w, appErr.Message, http.StatusBadRequest)
		case "unavailable", "busy":
			w.Header().Set("Retry-After", "1")
			http.Error(w, appErr.Message, http.StatusServiceUnavailable)
		default:
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(dest)
}

func decodeAnyMap(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	var payload map[string]any
	if err := decodeJSON(w, r, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func decodeStringMap(w http.ResponseWriter, r *http.Request) (map[string]string, error) {
	payload, err := decodeAnyMap(w, r)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(payload))
	for key, value := range payload {
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case float64:
			out[key] = strings.TrimRight(strings.TrimRight(strconv.FormatFloat(typed, 'f', -1, 64), "0"), ".")
		case bool:
			if typed {
				out[key] = "true"
			} else {
				out[key] = "false"
			}
		case nil:
			out[key] = ""
		default:
			raw, _ := json.Marshal(typed)
			out[key] = string(raw)
		}
	}
	return out, nil
}
