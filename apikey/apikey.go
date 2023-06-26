package apikey

import (
	"errors"
	"net/http"
	"strings"
)

var ErrAuthorizationRequired = errors.New("authorization required")
var ErrInvalidAuthorizationHeader = errors.New("invalid authorization header")
var ErrInvalidAPIKey = errors.New("invalid API key")

const bearerPrefix string = "Bearer "

type APIKey struct {
	Config []*APIKeyConfig
}

type Result struct {
	Principal  string
	StatusCode int
	Error      error // a nil error indicates the API Key is valid
}

func (r Result) IsValid() bool {
	return r.Error == nil && r.StatusCode == http.StatusOK
}

func (a *APIKey) Validate(req *http.Request) Result {
	authHeader := req.Header.Get("Authorization")
	if authHeader == "" {
		return Result{Error: ErrAuthorizationRequired, StatusCode: http.StatusUnauthorized}
	}
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		return Result{Error: ErrInvalidAuthorizationHeader, StatusCode: http.StatusBadRequest}
	}
	apiKey := strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))

	for _, entry := range a.Config {
		for _, key := range entry.Keys {
			if apiKey == key {
				return Result{
					Principal:  entry.Principal,
					StatusCode: http.StatusOK,
				}
			}
		}
	}

	return Result{Error: ErrInvalidAPIKey, StatusCode: http.StatusUnprocessableEntity}
}
