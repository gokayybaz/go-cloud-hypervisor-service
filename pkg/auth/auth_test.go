package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/org/ch-api/pkg/logging"
)

func TestMiddleware(t *testing.T) {
	secret := "super-secret-key"
	logger := logging.New("debug")
	cfg := Config{Secret: secret}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, ok := SubjectFromContext(r.Context())
		if !ok {
			http.Error(w, "no subject", http.StatusInternalServerError)
			return
		}
		roles, _ := RolesFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "sub=%s roles=%v", sub, roles)
	})

	mw := Middleware(cfg, logger)(handler)

	t.Run("missing header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
		if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
			t.Fatalf("expected application/problem+json, got %s", ct)
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-1",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte("wrong-secret"))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-1",
			"exp": time.Now().Add(-time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("missing subject", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("valid token", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub":   "user-1",
			"roles": []string{"admin", "operator"},
			"exp":   time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		expected := "sub=user-1 roles=[admin operator]"
		if rr.Body.String() != expected {
			t.Fatalf("expected body %q, got %q", expected, rr.Body.String())
		}
	})

	t.Run("valid token no roles", func(t *testing.T) {
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-2",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		expected := "sub=user-2 roles=[]"
		if rr.Body.String() != expected {
			t.Fatalf("expected body %q, got %q", expected, rr.Body.String())
		}
	})
}

func TestMiddlewareIssuerAndAudience(t *testing.T) {
	secret := "super-secret-key"
	logger := logging.New("debug")

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	t.Run("wrong issuer", func(t *testing.T) {
		cfg := Config{Secret: secret, Issuer: "expected-issuer"}
		mw := Middleware(cfg, logger)(handler)

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-1",
			"iss": "wrong-issuer",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("correct issuer", func(t *testing.T) {
		cfg := Config{Secret: secret, Issuer: "expected-issuer"}
		mw := Middleware(cfg, logger)(handler)

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-1",
			"iss": "expected-issuer",
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("wrong audience", func(t *testing.T) {
		cfg := Config{Secret: secret, Audience: "expected-audience"}
		mw := Middleware(cfg, logger)(handler)

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-1",
			"aud": []string{"wrong-audience"},
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("correct audience", func(t *testing.T) {
		cfg := Config{Secret: secret, Audience: "expected-audience"}
		mw := Middleware(cfg, logger)(handler)

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "user-1",
			"aud": []string{"expected-audience"},
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		ss, _ := token.SignedString([]byte(secret))
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("Authorization", "Bearer "+ss)
		rr := httptest.NewRecorder()
		mw.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})
}

func TestContextHelpers(t *testing.T) {
	ctx := context.Background()

	if _, ok := SubjectFromContext(ctx); ok {
		t.Fatal("expected no subject in empty context")
	}
	if _, ok := RolesFromContext(ctx); ok {
		t.Fatal("expected no roles in empty context")
	}

	ctx = WithSubject(ctx, "user-1")
	ctx = WithRoles(ctx, []string{"admin"})

	sub, ok := SubjectFromContext(ctx)
	if !ok || sub != "user-1" {
		t.Fatalf("expected subject user-1, got %s", sub)
	}
	roles, ok := RolesFromContext(ctx)
	if !ok || len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("expected roles [admin], got %v", roles)
	}
}
