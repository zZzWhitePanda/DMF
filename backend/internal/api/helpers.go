// Package api is the HTTP + WebSocket surface for the Dank Memer Farm
// backend. Routes under /auth/* handle Google sign-in; /api/* require
// authentication; /api/accounts/* additionally require a valid license.
package api

import (
	"encoding/json"
	"net/http"
)

// writeJSON serializes v as JSON with status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends a plain JSON error with HTTP status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
