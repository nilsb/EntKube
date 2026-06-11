package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey int

const claimsKey contextKey = 0

// Middleware validates the Authorization: Bearer <token> header and injects
// the parsed Claims into the request context.
// Returns 401 on missing/invalid token, 403 if admin is required and not set.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			http.Error(w, "missing authorization", http.StatusUnauthorized)
			return
		}
		claims, err := s.ValidateAccessToken(raw)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), claimsKey, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminMiddleware additionally requires IsAdmin == true.
func (s *Service) AdminMiddleware(next http.Handler) http.Handler {
	return s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromCtx(r.Context())
		if claims == nil || !claims.IsAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// ClaimsFromCtx retrieves Claims from the request context. Returns nil if absent.
func ClaimsFromCtx(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return after
	}
	return ""
}
