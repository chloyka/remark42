package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	gojwt "github.com/golang-jwt/jwt/v5"
	log "github.com/go-pkgz/lgr"

	"github.com/go-pkgz/auth/v2/token"
)

// FieldMappings defines how external JWT claims or JSON keys map to user fields
type FieldMappings struct {
	ID      string // claim/key for user ID
	Name    string // claim/key for display name
	Email   string // claim/key for email
	Picture string // claim/key for avatar URL
	Role    string // claim/key for role
}

// JWTAuthConfig holds configuration for the external JWT auth middleware
type JWTAuthConfig struct {
	Secret   string // shared secret (HMAC) or PEM-encoded public key (RSA/ECDSA)
	Algo     string // signing algorithm: HS256, RS256, ES256, etc.
	Header   string // HTTP header containing the JWT token
	Issuer   string // expected issuer claim (optional)
	Audience string // expected audience claim (optional)
	Map      FieldMappings
}

// ForwardAuthConfig holds configuration for the forward-auth middleware
type ForwardAuthConfig struct {
	Header string // HTTP header containing the decoded JWT payload as JSON
	Map    FieldMappings
}

// internalTokenCreator creates internal remark42 JWT tokens from user info.
// This is used by the JWT/forward-auth middleware to bridge external auth with
// the go-pkgz/auth middleware which expects its own JWT tokens.
type internalTokenCreator interface {
	Token(claims token.Claims) (string, error)
}

// JWTAuthMiddleware creates middleware that validates an external JWT token from the configured header
// and sets user info in the request context. If tokenCreator is provided, it also creates an internal
// remark42 JWT token and sets it as the X-JWT header so the downstream auth middleware recognizes the user.
// If no token header is present, the request passes through.
// Returns an error if the configuration is invalid (e.g., bad key material).
func JWTAuthMiddleware(cfg JWTAuthConfig, tokenCreator internalTokenCreator) (func(http.Handler) http.Handler, error) {
	keyFunc, err := makeKeyFunc(cfg.Algo, cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("JWT auth middleware configuration error: %w", err)
	}

	parserOpts := []gojwt.ParserOption{
		gojwt.WithValidMethods([]string{cfg.Algo}),
	}
	if cfg.Issuer != "" {
		parserOpts = append(parserOpts, gojwt.WithIssuer(cfg.Issuer))
	}
	if cfg.Audience != "" {
		parserOpts = append(parserOpts, gojwt.WithAudience(cfg.Audience))
	}
	parser := gojwt.NewParser(parserOpts...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := r.Header.Get(cfg.Header)
			// strip Bearer prefix if present (standard Authorization header format)
			tokenStr = strings.TrimPrefix(tokenStr, "Bearer ")
			tokenStr = strings.TrimSpace(tokenStr)
			if tokenStr == "" {
				next.ServeHTTP(w, r)
				return
			}

			tok, err := parser.Parse(tokenStr, keyFunc)
			if err != nil || !tok.Valid {
				log.Printf("[WARN] JWT auth: invalid token, %v", err)
				http.Error(w, "invalid JWT token", http.StatusUnauthorized)
				return
			}

			claims, ok := tok.Claims.(gojwt.MapClaims)
			if !ok {
				log.Printf("[WARN] JWT auth: can't extract claims")
				http.Error(w, "invalid JWT claims", http.StatusUnauthorized)
				return
			}

			user, err := extractUser(claims, cfg.Map, "jwt_")
			if err != nil {
				log.Printf("[WARN] JWT auth: %v", err)
				http.Error(w, "missing user ID claim", http.StatusUnauthorized)
				return
			}
			r = token.SetUserInfo(r, user)
			r = setInternalToken(r, user, tokenCreator)
			next.ServeHTTP(w, r)
		})
	}, nil
}

// ForwardAuthMiddleware creates middleware that reads decoded user claims from the configured header
// (as a JSON string) and sets user info in the request context. If tokenCreator is provided, it also
// creates an internal remark42 JWT token. If no header is present, passes through.
func ForwardAuthMiddleware(cfg ForwardAuthConfig, tokenCreator internalTokenCreator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			headerVal := r.Header.Get(cfg.Header)
			if headerVal == "" {
				next.ServeHTTP(w, r)
				return
			}

			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(headerVal), &payload); err != nil {
				log.Printf("[WARN] forward auth: can't parse JSON payload, %v", err)
				http.Error(w, "invalid forward-auth payload", http.StatusUnauthorized)
				return
			}

			user, err := extractUser(payload, cfg.Map, "forward_")
			if err != nil {
				log.Printf("[WARN] forward auth: %v", err)
				http.Error(w, "missing user ID in forward-auth payload", http.StatusUnauthorized)
				return
			}
			r = token.SetUserInfo(r, user)
			r = setInternalToken(r, user, tokenCreator)
			next.ServeHTTP(w, r)
		})
	}
}

// setInternalToken creates an internal remark42 JWT token for the given user and sets it
// on the request's X-JWT header so the downstream go-pkgz/auth middleware recognizes the user.
func setInternalToken(r *http.Request, user token.User, tc internalTokenCreator) *http.Request {
	if tc == nil {
		return r
	}

	// extract site ID from the request query parameter to use as the audience for the internal token
	siteID := r.URL.Query().Get("site")
	if siteID == "" {
		siteID = "remark42" // default site ID
	}

	claims := token.Claims{
		User: &user,
	}
	claims.Audience = []string{siteID}
	claims.Issuer = "remark42"

	tkn, err := tc.Token(claims)
	if err != nil {
		log.Printf("[WARN] external auth: can't create internal token for %s, %v", user.ID, err)
		return r
	}
	r.Header.Set("X-JWT", tkn)
	return r
}

// extractUser builds a token.User from a claims/payload map using the given field mappings.
// Returns an error if the user ID claim is missing or empty.
func extractUser(claims map[string]interface{}, m FieldMappings, prefix string) (token.User, error) {
	rawID := claimStr(claims, m.ID)
	if rawID == "" {
		return token.User{}, fmt.Errorf("user ID claim %q is missing or empty", m.ID)
	}

	user := token.User{
		ID:      prefix + rawID,
		Name:    claimStr(claims, m.Name),
		Email:   claimStr(claims, m.Email),
		Picture: claimStr(claims, m.Picture),
	}

	role := claimStr(claims, m.Role)
	if strings.EqualFold(role, "admin") {
		user.SetAdmin(true)
	}

	return user, nil
}

// claimStr extracts a string value from a claims map; returns empty string if missing or not a string
func claimStr(claims map[string]interface{}, key string) string {
	v, ok := claims[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

// makeKeyFunc creates a jwt.Keyfunc for the given algorithm and secret/key material
func makeKeyFunc(algo, secret string) (gojwt.Keyfunc, error) {
	switch {
	case strings.HasPrefix(algo, "HS"): // HMAC
		key := []byte(secret)
		return func(t *gojwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return key, nil
		}, nil

	case strings.HasPrefix(algo, "RS"): // RSA
		pubKey, err := gojwt.ParseRSAPublicKeyFromPEM([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("can't parse RSA public key: %w", err)
		}
		return func(t *gojwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*gojwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pubKey, nil
		}, nil

	case strings.HasPrefix(algo, "ES"): // ECDSA
		pubKey, err := gojwt.ParseECPublicKeyFromPEM([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("can't parse ECDSA public key: %w", err)
		}
		return func(t *gojwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*gojwt.SigningMethodECDSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return pubKey, nil
		}, nil

	default:
		return nil, fmt.Errorf("unsupported JWT algorithm: %s", algo)
	}
}
