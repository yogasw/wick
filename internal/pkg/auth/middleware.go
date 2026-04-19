package auth

import (
	"github.com/yogasw/wick/internal/pkg/api/resp"
	"net/http"
)

type middleware struct {
	secretKey string
}

func NewMiddleware(secretKey string) *middleware {
	return &middleware{
		secretKey: secretKey,
	}
}

func (m *middleware) StaticToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		tokenStr := r.Header.Get("Authorization")
		if tokenStr != m.secretKey {
			resp.WriteJSONFromError(w, &authError{authErrorUnauthorized})
			return
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
