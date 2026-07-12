// @index HTTP safety helpers for MCP server endpoints.
package mcp

import (
	"net/http"
)

// maxMCPRequestBodyBytes caps MCP HTTP request bodies at 10 MB.
const maxMCPRequestBodyBytes = 10 << 20

// @intent cap request memory usage before MCP handlers allocate or parse large request bodies.
// @ensures requests larger than the configured limit are rejected with 413.
// @sideEffect wraps the request body with a MaxBytesReader.
func LimitHTTPBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxMCPRequestBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxMCPRequestBodyBytes)
		next.ServeHTTP(w, r)
	})
}
