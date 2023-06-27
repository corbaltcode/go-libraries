package apikey

import (
	"log"
	"net/http"
)

func (a *APIKey) ValidateHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			result := a.Validate(r)
			if !result.IsValid() {
				log.Printf("API key failed validation: status %d, %s", result.StatusCode, result.Error)
				http.Error(w, result.Error.Error(), result.StatusCode)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}
