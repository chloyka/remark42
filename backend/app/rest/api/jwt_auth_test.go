package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/go-pkgz/auth/v2/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultJWTConfig() JWTAuthConfig {
	return JWTAuthConfig{
		Secret: "test-secret-key",
		Algo:   "HS256",
		Header: "X-Auth-Token",
		Map: FieldMappings{
			ID:      "sub",
			Name:    "name",
			Email:   "email",
			Picture: "picture",
			Role:    "role",
		},
	}
}

func defaultForwardConfig() ForwardAuthConfig {
	return ForwardAuthConfig{
		Header: "X-Forwarded-User",
		Map: FieldMappings{
			ID:      "sub",
			Name:    "name",
			Email:   "email",
			Picture: "picture",
			Role:    "role",
		},
	}
}

// makeHS256Token creates a signed HS256 JWT token with the given claims and secret
func makeHS256Token(t *testing.T, claims gojwt.MapClaims, secret string) string {
	t.Helper()
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(secret))
	require.NoError(t, err)
	return s
}

// userFromContext extracts the token.User from a request that passed through middleware
func userFromContext(r *http.Request) (token.User, error) {
	return token.GetUserInfo(r)
}

// handler that records the user from context
func captureUserHandler(t *testing.T, captured *token.User, called *bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		u, err := userFromContext(r)
		if err == nil {
			*captured = u
		}
		w.WriteHeader(http.StatusOK)
	})
}

// requireJWTMiddleware is a helper that creates JWTAuthMiddleware and fails the test on error
func requireJWTMiddleware(t *testing.T, cfg JWTAuthConfig) func(http.Handler) http.Handler {
	t.Helper()
	mw, err := JWTAuthMiddleware(cfg, nil)
	require.NoError(t, err)
	return mw
}

func TestJWTAuthMiddleware_ValidHS256Token(t *testing.T) {
	cfg := defaultJWTConfig()

	claims := gojwt.MapClaims{
		"sub":     "user123",
		"name":    "Test User",
		"email":   "test@example.com",
		"picture": "http://example.com/pic.png",
		"exp":     gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tokenStr := makeHS256Token(t, claims, cfg.Secret)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "jwt_user123", captured.ID)
	assert.Equal(t, "Test User", captured.Name)
	assert.Equal(t, "test@example.com", captured.Email)
	assert.Equal(t, "http://example.com/pic.png", captured.Picture)
	assert.False(t, captured.IsAdmin())
}

func TestJWTAuthMiddleware_BearerPrefix(t *testing.T) {
	cfg := defaultJWTConfig()

	claims := gojwt.MapClaims{
		"sub":  "bearer-user",
		"name": "Bearer User",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tokenStr := makeHS256Token(t, claims, cfg.Secret)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", "Bearer "+tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "jwt_bearer-user", captured.ID)
	assert.Equal(t, "Bearer User", captured.Name)
}

func TestJWTAuthMiddleware_InvalidToken(t *testing.T) {
	cfg := defaultJWTConfig()

	// sign with a different secret
	claims := gojwt.MapClaims{
		"sub": "user123",
		"exp": gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tokenStr := makeHS256Token(t, claims, "wrong-secret")

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called, "handler should not be called with invalid token")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestJWTAuthMiddleware_ExpiredToken(t *testing.T) {
	cfg := defaultJWTConfig()

	claims := gojwt.MapClaims{
		"sub": "user123",
		"exp": gojwt.NewNumericDate(time.Now().Add(-time.Hour)), // expired 1h ago
	}
	tokenStr := makeHS256Token(t, claims, cfg.Secret)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called, "handler should not be called with expired token")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestJWTAuthMiddleware_CustomClaimMappings(t *testing.T) {
	cfg := defaultJWTConfig()
	cfg.Map = FieldMappings{
		ID:      "user_id",
		Name:    "display_name",
		Email:   "mail",
		Picture: "avatar",
		Role:    "access_level",
	}

	claims := gojwt.MapClaims{
		"user_id":      "custom-id-456",
		"display_name": "Custom User",
		"mail":         "custom@example.com",
		"avatar":       "http://example.com/avatar.jpg",
		"access_level": "user",
		"exp":          gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tokenStr := makeHS256Token(t, claims, cfg.Secret)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "jwt_custom-id-456", captured.ID)
	assert.Equal(t, "Custom User", captured.Name)
	assert.Equal(t, "custom@example.com", captured.Email)
	assert.Equal(t, "http://example.com/avatar.jpg", captured.Picture)
	assert.False(t, captured.IsAdmin())
}

func TestJWTAuthMiddleware_AdminRole(t *testing.T) {
	cfg := defaultJWTConfig()

	claims := gojwt.MapClaims{
		"sub":  "admin-user",
		"name": "Admin",
		"role": "admin",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tokenStr := makeHS256Token(t, claims, cfg.Secret)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, captured.IsAdmin(), "user with admin role should be admin")
}

func TestJWTAuthMiddleware_AdminRoleCaseInsensitive(t *testing.T) {
	cfg := defaultJWTConfig()

	claims := gojwt.MapClaims{
		"sub":  "admin-user-2",
		"name": "Admin2",
		"role": "Admin",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tokenStr := makeHS256Token(t, claims, cfg.Secret)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.True(t, captured.IsAdmin(), "admin role should be case-insensitive")
}

func TestJWTAuthMiddleware_MissingHeader(t *testing.T) {
	cfg := defaultJWTConfig()

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// no X-Auth-Token header set
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called, "handler should be called (pass-through) when no token header")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "", captured.ID, "no user should be set when header is missing")
}

func TestJWTAuthMiddleware_MissingIDClaim(t *testing.T) {
	cfg := defaultJWTConfig()

	claims := gojwt.MapClaims{
		"name": "No ID User",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tokenStr := makeHS256Token(t, claims, cfg.Secret)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called, "handler should not be called when ID claim is missing")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestJWTAuthMiddleware_ConfigError(t *testing.T) {
	cfg := JWTAuthConfig{
		Secret: "not-a-valid-pem",
		Algo:   "RS256",
		Header: "X-Auth-Token",
		Map: FieldMappings{
			ID: "sub",
		},
	}
	_, err := JWTAuthMiddleware(cfg, nil)
	assert.Error(t, err, "should return error for invalid RSA key material")
}

func TestJWTAuthMiddleware_RS256(t *testing.T) {
	// generate RSA key pair
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})

	cfg := JWTAuthConfig{
		Secret: string(pubPEM),
		Algo:   "RS256",
		Header: "X-Auth-Token",
		Map: FieldMappings{
			ID:      "sub",
			Name:    "name",
			Email:   "email",
			Picture: "picture",
			Role:    "role",
		},
	}

	claims := gojwt.MapClaims{
		"sub":  "rsa-user",
		"name": "RSA User",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodRS256, claims)
	tokenStr, err := tok.SignedString(privKey)
	require.NoError(t, err)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "jwt_rsa-user", captured.ID)
	assert.Equal(t, "RSA User", captured.Name)
}

func TestJWTAuthMiddleware_IssuerValidation(t *testing.T) {
	cfg := defaultJWTConfig()
	cfg.Issuer = "my-issuer"

	t.Run("valid issuer", func(t *testing.T) {
		claims := gojwt.MapClaims{
			"sub": "user1",
			"iss": "my-issuer",
			"exp": gojwt.NewNumericDate(time.Now().Add(time.Hour)),
		}
		tokenStr := makeHS256Token(t, claims, cfg.Secret)

		var captured token.User
		var called bool
		handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-Auth-Token", tokenStr)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.True(t, called)
		assert.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "jwt_user1", captured.ID)
	})

	t.Run("invalid issuer", func(t *testing.T) {
		claims := gojwt.MapClaims{
			"sub": "user2",
			"iss": "wrong-issuer",
			"exp": gojwt.NewNumericDate(time.Now().Add(time.Hour)),
		}
		tokenStr := makeHS256Token(t, claims, cfg.Secret)

		var captured token.User
		var called bool
		handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-Auth-Token", tokenStr)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.False(t, called)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestJWTAuthMiddleware_AudienceValidation(t *testing.T) {
	cfg := defaultJWTConfig()
	cfg.Audience = "my-app"

	t.Run("valid audience", func(t *testing.T) {
		claims := gojwt.MapClaims{
			"sub": "user1",
			"aud": "my-app",
			"exp": gojwt.NewNumericDate(time.Now().Add(time.Hour)),
		}
		tokenStr := makeHS256Token(t, claims, cfg.Secret)

		var captured token.User
		var called bool
		handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-Auth-Token", tokenStr)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.True(t, called)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("invalid audience", func(t *testing.T) {
		claims := gojwt.MapClaims{
			"sub": "user2",
			"aud": "wrong-app",
			"exp": gojwt.NewNumericDate(time.Now().Add(time.Hour)),
		}
		tokenStr := makeHS256Token(t, claims, cfg.Secret)

		var captured token.User
		var called bool
		handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.Header.Set("X-Auth-Token", tokenStr)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		assert.False(t, called)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestJWTAuthMiddleware_ES256(t *testing.T) {
	// generate ECDSA key pair
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})

	cfg := JWTAuthConfig{
		Secret: string(pubPEM),
		Algo:   "ES256",
		Header: "X-Auth-Token",
		Map: FieldMappings{
			ID:      "sub",
			Name:    "name",
			Email:   "email",
			Picture: "picture",
			Role:    "role",
		},
	}

	claims := gojwt.MapClaims{
		"sub":  "ecdsa-user",
		"name": "ECDSA User",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodES256, claims)
	tokenStr, err := tok.SignedString(privKey)
	require.NoError(t, err)

	var captured token.User
	var called bool
	handler := requireJWTMiddleware(t, cfg)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Auth-Token", tokenStr)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "jwt_ecdsa-user", captured.ID)
	assert.Equal(t, "ECDSA User", captured.Name)
}

// Forward-auth middleware tests

func TestForwardAuthMiddleware_ValidJSON(t *testing.T) {
	cfg := defaultForwardConfig()

	payload := map[string]interface{}{
		"sub":     "fwd-user-1",
		"name":    "Forward User",
		"email":   "fwd@example.com",
		"picture": "http://example.com/fwd.png",
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	var captured token.User
	var called bool
	handler := ForwardAuthMiddleware(cfg, nil)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Forwarded-User", string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "forward_fwd-user-1", captured.ID)
	assert.Equal(t, "Forward User", captured.Name)
	assert.Equal(t, "fwd@example.com", captured.Email)
	assert.Equal(t, "http://example.com/fwd.png", captured.Picture)
	assert.False(t, captured.IsAdmin())
}

func TestForwardAuthMiddleware_AdminRole(t *testing.T) {
	cfg := defaultForwardConfig()

	payload := map[string]interface{}{
		"sub":  "fwd-admin",
		"name": "Forward Admin",
		"role": "ADMIN", // test case-insensitive
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	var captured token.User
	var called bool
	handler := ForwardAuthMiddleware(cfg, nil)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Forwarded-User", string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.True(t, captured.IsAdmin(), "user with ADMIN role should be admin")
}

func TestForwardAuthMiddleware_CustomMappings(t *testing.T) {
	cfg := ForwardAuthConfig{
		Header: "X-User-Info",
		Map: FieldMappings{
			ID:      "uid",
			Name:    "full_name",
			Email:   "mail",
			Picture: "photo",
			Role:    "access",
		},
	}

	payload := map[string]interface{}{
		"uid":       "custom-fwd-123",
		"full_name": "Custom Forward User",
		"mail":      "custom@example.com",
		"photo":     "http://example.com/custom.jpg",
		"access":    "user",
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	var captured token.User
	var called bool
	handler := ForwardAuthMiddleware(cfg, nil)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-User-Info", string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "forward_custom-fwd-123", captured.ID)
	assert.Equal(t, "Custom Forward User", captured.Name)
	assert.Equal(t, "custom@example.com", captured.Email)
	assert.Equal(t, "http://example.com/custom.jpg", captured.Picture)
	assert.False(t, captured.IsAdmin())
}

func TestForwardAuthMiddleware_MissingHeader(t *testing.T) {
	cfg := defaultForwardConfig()

	var captured token.User
	var called bool
	handler := ForwardAuthMiddleware(cfg, nil)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// no header set
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called, "handler should be called (pass-through) when no header")
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "", captured.ID, "no user should be set when header is missing")
}

func TestForwardAuthMiddleware_InvalidJSON(t *testing.T) {
	cfg := defaultForwardConfig()

	var captured token.User
	var called bool
	handler := ForwardAuthMiddleware(cfg, nil)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Forwarded-User", "not-valid-json{{{")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called, "handler should not be called with invalid JSON")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestForwardAuthMiddleware_MissingIDKey(t *testing.T) {
	cfg := defaultForwardConfig()

	payload := map[string]interface{}{
		"name": "No ID User",
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	var captured token.User
	var called bool
	handler := ForwardAuthMiddleware(cfg, nil)(captureUserHandler(t, &captured, &called))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Forwarded-User", string(payloadBytes))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called, "handler should not be called when ID key is missing")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestExtractUser(t *testing.T) {
	m := FieldMappings{ID: "sub", Name: "name", Email: "email", Picture: "picture", Role: "role"}

	t.Run("all fields present", func(t *testing.T) {
		claims := map[string]interface{}{
			"sub":     "id1",
			"name":    "User 1",
			"email":   "u1@test.com",
			"picture": "http://pic.com",
			"role":    "admin",
		}
		user, err := extractUser(claims, m, "test_")
		require.NoError(t, err)
		assert.Equal(t, "test_id1", user.ID)
		assert.Equal(t, "User 1", user.Name)
		assert.Equal(t, "u1@test.com", user.Email)
		assert.Equal(t, "http://pic.com", user.Picture)
		assert.True(t, user.IsAdmin())
	})

	t.Run("missing optional fields", func(t *testing.T) {
		claims := map[string]interface{}{
			"sub": "id2",
		}
		user, err := extractUser(claims, m, "test_")
		require.NoError(t, err)
		assert.Equal(t, "test_id2", user.ID)
		assert.Equal(t, "", user.Name)
		assert.Equal(t, "", user.Email)
		assert.Equal(t, "", user.Picture)
		assert.False(t, user.IsAdmin())
	})

	t.Run("missing ID returns error", func(t *testing.T) {
		claims := map[string]interface{}{
			"name": "No ID",
		}
		_, err := extractUser(claims, m, "test_")
		assert.Error(t, err)
	})

	t.Run("non-admin role", func(t *testing.T) {
		claims := map[string]interface{}{
			"sub":  "id3",
			"role": "viewer",
		}
		user, err := extractUser(claims, m, "test_")
		require.NoError(t, err)
		assert.False(t, user.IsAdmin())
	})
}

func TestClaimStr(t *testing.T) {
	claims := map[string]interface{}{
		"string_val": "hello",
		"int_val":    42,
		"float_val":  3.14,
		"bool_val":   true,
	}

	assert.Equal(t, "hello", claimStr(claims, "string_val"))
	assert.Equal(t, "42", claimStr(claims, "int_val"))
	assert.Equal(t, "3.14", claimStr(claims, "float_val"))
	assert.Equal(t, "true", claimStr(claims, "bool_val"))
	assert.Equal(t, "", claimStr(claims, "missing"))
}
