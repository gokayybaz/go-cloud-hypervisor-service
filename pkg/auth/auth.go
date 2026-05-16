// Package auth provides JWT authentication middleware for the HTTP API.
package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/org/ch-api/pkg/api/problem"
	"github.com/org/ch-api/pkg/logging"
)

// ---------------------------------------------------------------------------
// Context keys
// ---------------------------------------------------------------------------

type contextKey int

const (
	subjectKey contextKey = iota
	rolesKey
)

// ---------------------------------------------------------------------------
// Claims
// ---------------------------------------------------------------------------

// Claims extends jwt.RegisteredClaims with application-specific fields.
type Claims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles,omitempty"`
}

// ---------------------------------------------------------------------------
// Context helpers
// ---------------------------------------------------------------------------

// WithSubject returns a new context with the subject value.
func WithSubject(ctx context.Context, sub string) context.Context {
	return context.WithValue(ctx, subjectKey, sub)
}

// SubjectFromContext extracts the subject from the context.
// The second return value indicates whether the subject was present.
func SubjectFromContext(ctx context.Context) (string, bool) {
	sub, ok := ctx.Value(subjectKey).(string)
	return sub, ok
}

// WithRoles returns a new context with the roles value.
func WithRoles(ctx context.Context, roles []string) context.Context {
	return context.WithValue(ctx, rolesKey, roles)
}

// RolesFromContext extracts the roles from the context.
// The second return value indicates whether the roles were present.
func RolesFromContext(ctx context.Context) ([]string, bool) {
	roles, ok := ctx.Value(rolesKey).([]string)
	return roles, ok
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// Config holds tunable parameters for JWT validation.
type Config struct {
	// Secret is the symmetric signing key (HS256).  Must be non-empty when
	// auth is enabled.
	Secret string

	// Issuer is the expected iss claim.  Empty means any issuer is accepted.
	Issuer string

	// Audience is the expected aud claim.  Empty means any audience is accepted.
	Audience string

	// RBACEnabled controls whether role-based access control is enforced.
	// When true (default) and Secret is set, the RBAC middleware is mounted
	// on the v1 router.
	RBACEnabled bool
}

// Middleware returns a chi-compatible HTTP middleware that validates JWT
// Bearer tokens.
//
// Validation rules:
//   - Authorization header must be present and start with "Bearer "
//   - Token must be parsable and the signature must verify (HS256)
//   - Token must not be expired (exp claim)
//   - Token must not be used before nbf (if present)
//   - The sub claim must be present
//
// On success the sub and roles claims are injected into the request context.
// On failure a 401 Unauthorized response with an RFC 7807 Problem Details
// body is written and the request does not propagate.
func Middleware(cfg Config, logger logging.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				problem.Unauthorized(r.URL.Path, "missing authorization header").Write(w)
				return
			}

			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				problem.Unauthorized(r.URL.Path, "invalid authorization header format").Write(w)
				return
			}
			tokenString := parts[1]

			claims := &Claims{}
			token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return []byte(cfg.Secret), nil
			}, jwt.WithValidMethods([]string{"HS256"}))

			if err != nil {
				problem.Unauthorized(r.URL.Path, fmt.Sprintf("invalid token: %v", err)).Write(w)
				return
			}
			if !token.Valid {
				problem.Unauthorized(r.URL.Path, "invalid token").Write(w)
				return
			}

			// Issuer validation
			if cfg.Issuer != "" {
				iss, err := token.Claims.GetIssuer()
				if err != nil || iss != cfg.Issuer {
					problem.Unauthorized(r.URL.Path, "invalid issuer").Write(w)
					return
				}
			}

			// Audience validation
			if cfg.Audience != "" {
				aud, err := token.Claims.GetAudience()
				if err != nil || !contains(aud, cfg.Audience) {
					problem.Unauthorized(r.URL.Path, "invalid audience").Write(w)
					return
				}
			}

			// Subject claim is required.
			sub, err := token.Claims.GetSubject()
			if err != nil || sub == "" {
				problem.Unauthorized(r.URL.Path, "missing subject claim").Write(w)
				return
			}

			// Expiry is already validated by jwt.ParseWithClaims, but defensively
			// double-check if the library is configured with leeway in the future.
			exp, err := token.Claims.GetExpirationTime()
			if err == nil && exp != nil && exp.Before(time.Now()) {
				problem.Unauthorized(r.URL.Path, "token expired").Write(w)
				return
			}

			ctx := WithSubject(r.Context(), sub)
			ctx = WithRoles(ctx, claims.Roles)

			log := logger.WithContext(ctx)
			log.Debug("request authenticated", "sub", sub, "roles", claims.Roles)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
