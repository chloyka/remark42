package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/go-pkgz/auth/v2/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/remark42/backend/app/store"
)

func TestIntegration_JWTAuth_UserEndpoint(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		srv.JWTAuthConf = JWTAuthConfig{
			Secret: "integration-test-secret",
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
		srv.Authenticator.AddDirectProvider("jwt", provider.CredCheckerFunc(func(_, _ string) (bool, error) {
			return false, nil
		}))
	})
	defer teardown()

	// create a valid external JWT token
	claims := gojwt.MapClaims{
		"sub":     "user-123",
		"name":    "JWT Test User",
		"email":   "jwtuser@example.com",
		"picture": "http://example.com/jwt-pic.png",
		"exp":     gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte("integration-test-secret"))
	require.NoError(t, err)

	// send request to /api/v1/user with the JWT token
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Auth-Token", tokenStr)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var user store.User
	err = json.Unmarshal(body, &user)
	require.NoError(t, err)

	assert.Equal(t, "jwt_user-123", user.ID)
	assert.Equal(t, "JWT Test User", user.Name)
	assert.False(t, user.Admin)
}

func TestIntegration_ForwardAuth_UserEndpoint(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		srv.ForwardAuthConf = ForwardAuthConfig{
			Header: "X-Forwarded-User",
			Map: FieldMappings{
				ID:      "sub",
				Name:    "name",
				Email:   "email",
				Picture: "picture",
				Role:    "role",
			},
		}
		srv.Authenticator.AddDirectProvider("forward", provider.CredCheckerFunc(func(_, _ string) (bool, error) {
			return false, nil
		}))
	})
	defer teardown()

	// create JSON payload for forward-auth header
	payload := map[string]interface{}{
		"sub":     "fwd-user-456",
		"name":    "Forward Test User",
		"email":   "fwduser@example.com",
		"picture": "http://example.com/fwd-pic.png",
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Forwarded-User", string(payloadBytes))

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var user store.User
	err = json.Unmarshal(body, &user)
	require.NoError(t, err)

	assert.Equal(t, "forward_fwd-user-456", user.ID)
	assert.Equal(t, "Forward Test User", user.Name)
	assert.False(t, user.Admin)
}

func TestIntegration_JWTAuth_AdminRole(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		srv.JWTAuthConf = JWTAuthConfig{
			Secret: "admin-test-secret",
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
		srv.Authenticator.AddDirectProvider("jwt", provider.CredCheckerFunc(func(_, _ string) (bool, error) {
			return false, nil
		}))
	})
	defer teardown()

	// create JWT with admin role
	claims := gojwt.MapClaims{
		"sub":  "admin-jwt-user",
		"name": "JWT Admin",
		"role": "admin",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte("admin-test-secret"))
	require.NoError(t, err)

	// verify user endpoint shows admin status
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Auth-Token", tokenStr)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var user store.User
	err = json.Unmarshal(body, &user)
	require.NoError(t, err)

	assert.Equal(t, "jwt_admin-jwt-user", user.ID)
	assert.Equal(t, "JWT Admin", user.Name)
	assert.True(t, user.Admin, "user with admin role should be admin")

	// verify admin can access admin-only endpoints (e.g., blocked users list)
	req2, err := http.NewRequest("GET", ts.URL+"/api/v1/admin/blocked?site=remark42", http.NoBody)
	require.NoError(t, err)
	req2.Header.Set("X-Auth-Token", tokenStr)

	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode, "admin user should access admin endpoints")
}

func TestIntegration_ForwardAuth_AdminRole(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		srv.ForwardAuthConf = ForwardAuthConfig{
			Header: "X-Forwarded-User",
			Map: FieldMappings{
				ID:      "sub",
				Name:    "name",
				Email:   "email",
				Picture: "picture",
				Role:    "role",
			},
		}
		srv.Authenticator.AddDirectProvider("forward", provider.CredCheckerFunc(func(_, _ string) (bool, error) {
			return false, nil
		}))
	})
	defer teardown()

	// create JSON payload with admin role
	payload := map[string]interface{}{
		"sub":  "fwd-admin-user",
		"name": "Forward Admin",
		"role": "Admin", // test case-insensitive
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Forwarded-User", string(payloadBytes))

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var user store.User
	err = json.Unmarshal(body, &user)
	require.NoError(t, err)

	assert.Equal(t, "forward_fwd-admin-user", user.ID)
	assert.True(t, user.Admin, "forward-auth user with Admin role should be admin")
}

func TestIntegration_JWTAndOAuthCoexist(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		srv.JWTAuthConf = JWTAuthConfig{
			Secret: "coexist-test-secret",
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
		srv.Authenticator.AddDirectProvider("jwt", provider.CredCheckerFunc(func(_, _ string) (bool, error) {
			return false, nil
		}))
	})
	defer teardown()

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()

	// test 1: JWT auth works
	claims := gojwt.MapClaims{
		"sub":  "jwt-coexist-user",
		"name": "JWT Coexist User",
		"exp":  gojwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims)
	tokenStr, err := tok.SignedString([]byte("coexist-test-secret"))
	require.NoError(t, err)

	req, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("X-Auth-Token", tokenStr)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var user store.User
	err = json.Unmarshal(body, &user)
	require.NoError(t, err)
	assert.Equal(t, "jwt_jwt-coexist-user", user.ID)

	// test 2: standard OAuth (dev token) still works alongside JWT auth
	req2, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)
	req2.Header.Set("X-JWT", devToken) // use the standard remark42 dev token

	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err)

	var user2 store.User
	err = json.Unmarshal(body2, &user2)
	require.NoError(t, err)
	assert.Equal(t, "provider1_dev", user2.ID, "standard OAuth user should still work")
}

func TestIntegration_PassthroughWithoutHeaders(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		srv.JWTAuthConf = JWTAuthConfig{
			Secret: "passthrough-test-secret",
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
		srv.ForwardAuthConf = ForwardAuthConfig{
			Header: "X-Forwarded-User",
			Map: FieldMappings{
				ID:      "sub",
				Name:    "name",
				Email:   "email",
				Picture: "picture",
				Role:    "role",
			},
		}
		srv.Authenticator.AddDirectProvider("jwt", provider.CredCheckerFunc(func(_, _ string) (bool, error) {
			return false, nil
		}))
		srv.Authenticator.AddDirectProvider("forward", provider.CredCheckerFunc(func(_, _ string) (bool, error) {
			return false, nil
		}))
	})
	defer teardown()

	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()

	// request without any auth headers should fall through to standard auth
	// which will reject the request as unauthorized
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
		"request without JWT/forward-auth headers should be rejected by standard auth middleware")

	// request with standard remark42 dev token should still work (pass-through)
	req2, err := http.NewRequest("GET", ts.URL+"/api/v1/user?site=remark42", http.NoBody)
	require.NoError(t, err)
	req2.Header.Set("X-JWT", devToken)

	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusOK, resp2.StatusCode,
		"standard auth should work when JWT/forward-auth headers are absent")
}
