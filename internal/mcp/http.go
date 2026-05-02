package mcp

import (
	"net/http"
)

const maxMCPRequestBodyBytes = 10 << 20 // 10 MB

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
