package auth

import (
	"encoding/json"
	"net/http"
)

// writeJSONError sends a plain JSON error with HTTP status.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSONErrorCode(w, status, "", msg)
}

// writeJSONErrorCode sends a JSON error with an optional machine-readable
// code field so the frontend can switch on specific failure modes
// (no_license, hwid_mismatch, etc.) without string-matching the message.
func writeJSONErrorCode(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}
