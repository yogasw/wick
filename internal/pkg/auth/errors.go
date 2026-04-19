package auth

import "net/http"

type authError struct {
	code int
}

const (
	authErrorUnauthorized = iota
)

func (e *authError) Error() string {
	switch e.code {
	case authErrorUnauthorized:
		return "Unauthorized"
	default:
		return "Unknown error code"
	}
}

func (e *authError) HTTPStatusCode() int {
	switch e.code {
	case authErrorUnauthorized:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}
