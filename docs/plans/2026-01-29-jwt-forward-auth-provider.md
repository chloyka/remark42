# JWT / Forward Auth Provider for Remark42

Add a transparent (no-UI) JWT authentication provider and a forward-auth mode. Both allow remark42 to work behind a reverse proxy (Traefik, Nginx, etc.) where users are pre-authenticated externally. In JWT mode, remark42 receives a signed JWT token in a configurable header, validates it, and extracts user info via field mappings. In forward-auth mode, remark42 receives already-decoded user claims in HTTP headers and trusts them directly.

## Context

- Files involved: `backend/app/cmd/server.go`, `backend/app/rest/api/rest.go`, new middleware file, site docs, frontend (no UI changes, only config types update)
- Related patterns: existing auth providers in `server.go:addAuthProviders()`, middleware chain in `rest.go:routes()`, go-pkgz/auth middleware
- Dependencies: `github.com/golang-jwt/jwt/v5` (already vendored)

## Approach

- **Testing approach**: Code first, then tests for each task
- Complete each task fully before moving to the next
- The JWT/forward-auth middleware sits **before** the go-pkgz/auth middleware in the chain. When it detects a valid external JWT or forward-auth headers, it creates a remark42 internal JWT token (via `auth.Service.TokenService().Set()`) and sets the cookie/header so the standard auth middleware recognizes the user
- No frontend UI changes are needed (transparent provider, no login button). Only the frontend TypeScript types need updating to include `"jwt"` in the provider list so it's not treated as unknown
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Configuration

### JWT Mode
Environment variables (all prefixed `AUTH_JWT_`):
- `AUTH_JWT_SECRET` (string, required to enable JWT mode) - shared secret for HMAC or public key for RSA/ECDSA
- `AUTH_JWT_ALGO` (string, default `HS256`) - JWT signing algorithm (`HS256`, `HS384`, `HS512`, `RS256`, `RS384`, `RS512`, `ES256`, `ES384`, `ES512`)
- `AUTH_JWT_HEADER` (string, default `X-Auth-Token`) - HTTP header containing the JWT token
- `AUTH_JWT_ISSUER` (string, optional) - expected issuer claim for validation
- `AUTH_JWT_AUDIENCE` (string, optional) - expected audience claim for validation

### JWT Field Mappings
- `AUTH_JWT_MAP_ID` (string, default `sub`) - JWT claim for user ID
- `AUTH_JWT_MAP_NAME` (string, default `name`) - JWT claim for display name
- `AUTH_JWT_MAP_EMAIL` (string, default `email`) - JWT claim for email
- `AUTH_JWT_MAP_PICTURE` (string, default `picture`) - JWT claim for avatar URL
- `AUTH_JWT_MAP_ROLE` (string, default `role`) - JWT claim for role. Value `admin` grants admin privileges

### Forward Auth Mode
Environment variables (all prefixed `AUTH_FORWARD_`):
- `AUTH_FORWARD_HEADER` (string, required to enable forward-auth mode) - HTTP header containing the decoded JWT payload as JSON
- `AUTH_FORWARD_MAP_ID` (string, default `sub`) - JSON key for user ID
- `AUTH_FORWARD_MAP_NAME` (string, default `name`) - JSON key for display name
- `AUTH_FORWARD_MAP_EMAIL` (string, default `email`) - JSON key for email
- `AUTH_FORWARD_MAP_PICTURE` (string, default `picture`) - JSON key for avatar URL
- `AUTH_FORWARD_MAP_ROLE` (string, default `role`) - JSON key for role. Value `admin` grants admin privileges

### Role Mapping
The `role` field extracted from JWT claims or forward-auth headers determines admin status:
- If the value equals `"admin"` (case-insensitive), the user gets admin privileges in remark42
- Any other value or missing role means regular user

---

## Task 1: Add configuration structs and CLI flags

**Files:**
- Modify: `backend/app/cmd/server.go`

### Steps
- [x] Add `JWTAuth` struct inside the `Auth` struct with fields: `Secret`, `Algo`, `Header`, `Issuer`, `Audience`, and mapping sub-struct (`MapID`, `MapName`, `MapEmail`, `MapPicture`, `MapRole`) with proper `long`, `env`, `default`, and `description` tags
- [x] Add `ForwardAuth` struct inside the `Auth` struct with fields: `Header` and mapping sub-struct (same fields as JWT mappings)
- [x] Verify the struct compiles and env vars parse correctly
- [x] Write tests: add test in `backend/app/cmd/server_test.go` that sets JWT/forward-auth config fields and verifies they are populated
- [x] Run `cd backend/app && go test -run TestJWT -count 1 ./cmd/` - must pass

---

## Task 2: Create JWT/forward-auth middleware

**Files:**
- Create: `backend/app/rest/api/jwt_auth.go`
- Create: `backend/app/rest/api/jwt_auth_test.go`

### Steps
- [x] Create `JWTAuthConfig` struct holding all configuration for the middleware (secret, algo, header, issuer, audience, field mappings)
- [x] Create `ForwardAuthConfig` struct holding forward-auth configuration (header, field mappings)
- [x] Implement `JWTAuthMiddleware(cfg JWTAuthConfig, tokenService TokenService) func(http.Handler) http.Handler`:
  - Check for JWT in the configured header
  - If no header present, pass through to next handler (non-blocking, the user might use standard auth)
  - Parse and validate the JWT using the configured algorithm and secret
  - If issuer/audience are configured, validate them
  - Extract user fields using the configured claim mappings
  - Generate user ID with `jwt_` prefix + mapped ID value (to namespace external users)
  - Check role mapping: if role claim == "admin" (case-insensitive), set admin flag
  - Create internal remark42 token via `token.User` and set it in the request context using `token.SetUserInfo()`
- [x] Implement `ForwardAuthMiddleware(cfg ForwardAuthConfig) func(http.Handler) http.Handler`:
  - Check for decoded payload in the configured header (JSON string)
  - If no header present, pass through
  - Parse JSON payload
  - Extract user fields using the configured key mappings
  - Generate user ID with `forward_` prefix + mapped ID value
  - Check role mapping same as JWT mode
  - Set user in request context using `token.SetUserInfo()`
- [x] Write comprehensive tests in `jwt_auth_test.go`:
  - Test JWT middleware with valid HS256 token
  - Test JWT middleware with invalid token (bad signature)
  - Test JWT middleware with expired token
  - Test JWT middleware with custom claim mappings
  - Test JWT middleware with admin role
  - Test JWT middleware with missing header (pass-through)
  - Test JWT middleware with RS256 algorithm
  - Test JWT middleware with issuer/audience validation
  - Test forward-auth middleware with valid JSON header
  - Test forward-auth middleware with admin role
  - Test forward-auth middleware with custom mappings
  - Test forward-auth middleware with missing header (pass-through)
  - Test forward-auth middleware with invalid JSON
- [x] Run `cd backend/app && go test -count 1 ./rest/api/` - must pass

---

## Task 3: Wire middleware into the server and API routes

**Files:**
- Modify: `backend/app/cmd/server.go`
- Modify: `backend/app/rest/api/rest.go`

### Steps
- [x] In `server.go`, pass JWT/forward-auth config to the `Rest` struct when creating it. Add `JWTAuthConfig` and `ForwardAuthConfig` fields to the `Rest` struct in `rest.go`
- [x] In `rest.go:routes()`, add the JWT auth middleware and/or forward-auth middleware to the router BEFORE the go-pkgz/auth middleware (at the top of the middleware chain, after CORS). Only add if the respective config is enabled (Secret non-empty for JWT, Header non-empty for forward-auth)
- [x] In `addAuthProviders()` in `server.go`, increment `providersCount` when JWT or forward-auth is enabled (so the "no auth providers" warning doesn't trigger)
- [x] Log startup messages: `[INFO] JWT auth enabled, header: X-Auth-Token, algo: HS256` and similar for forward-auth
- [x] Write/update tests in `server_test.go`:
  - Test that JWT auth config gets passed to Rest correctly
  - Test addAuthProviders counts JWT/forward-auth as providers
- [x] Run `cd backend/app && go test -count 1 ./cmd/ ./rest/api/` - must pass

---

## Task 4: Integration test - end-to-end auth flow

**Files:**
- Create: `backend/app/rest/api/jwt_auth_integration_test.go`

### Steps
- [x] Write an integration test that starts a full Rest server with JWT auth configured, sends a request with a valid JWT token in the configured header, and verifies the user is authenticated (can access protected endpoints like `/api/v1/user`)
- [x] Write an integration test for forward-auth mode: start server with forward-auth configured, send request with JSON payload header, verify user is authenticated
- [x] Test that admin role mapping works end-to-end: user with admin role can access admin endpoints
- [x] Test that JWT and forward-auth can coexist with standard OAuth providers (both work)
- [x] Test that requests without JWT/forward-auth headers fall through to standard auth
- [x] Run `cd backend/app && go test -count 1 ./rest/api/` - must pass

---

## Task 5: Update frontend types

**Files:**
- Modify: `frontend/apps/remark42/app/components/auth/auth.utils.ts` or relevant provider constants file

### Steps
- [x] Add `"jwt"` and `"forward_auth"` to the known provider list so TypeScript doesn't flag them as unknown (even though they won't render UI buttons)
- [x] Ensure that if these providers appear in the `auth_providers` config response, they are silently ignored in the UI (no button rendered) - verify the existing logic already handles unknown providers gracefully, or add a filter
- [x] Write/update frontend test if applicable to verify unknown/transparent providers don't break the auth panel
- [x] Run `cd frontend && pnpm test` - must pass

---

## Task 6: Update site documentation

**Files:**
- Modify: `site/src/docs/configuration/authorization/index.md`
- Modify: `site/src/docs/configuration/parameters/index.md`

### Steps
- [ ] Add a new section "JWT Authentication" to `authorization/index.md` explaining:
  - What JWT auth is and when to use it (behind reverse proxy with JWT-based SSO)
  - All `AUTH_JWT_*` environment variables with descriptions and defaults
  - Example: using with Traefik forward auth or Authelia
  - Role mapping explanation
- [ ] Add a new section "Forward Auth" to `authorization/index.md` explaining:
  - What forward auth is (reverse proxy decodes JWT and passes claims in header)
  - All `AUTH_FORWARD_*` environment variables with descriptions and defaults
  - Example: Traefik forward auth + Authelia passing decoded user info
  - Role mapping explanation
- [ ] Add all new `AUTH_JWT_*` and `AUTH_FORWARD_*` parameters to the parameters table in `parameters/index.md`
- [ ] Verify docs build correctly (if there's a build command)

---

## Task 7: Lint and final verification

**Files:**
- All modified files

### Steps
- [ ] Run `cd backend && golangci-lint run`
- [ ] Run `cd backend/_example/memory_store && golangci-lint run --config ../../.golangci.yml`
- [ ] Run `cd backend/app && go test -timeout=60s -count 1 ./...`
- [ ] Run `cd backend/_example/memory_store && go test -race ./... && go build -race ./...`
- [ ] Run `cd frontend && pnpm lint`
- [ ] Run `cd frontend && pnpm test`
- [ ] Fix any issues found

---

## Validation Checklist

- [ ] Manual test: configure JWT auth with a test secret, generate a JWT with `sub`, `name`, `role=admin` claims, send request with the token in the configured header, verify user is authenticated as admin
- [ ] Manual test: configure forward-auth, send request with JSON user payload in header, verify authentication
- [ ] Run full backend test suite: `cd backend/app && go test -timeout=60s -count 1 ./...`
- [ ] Run backend linter: `cd backend && golangci-lint run`
- [ ] Run example tests: `cd backend/_example/memory_store && go test -race ./... && go build -race ./...`
- [ ] Run example linter: `cd backend/_example/memory_store && golangci-lint run --config ../../.golangci.yml`
- [ ] Run frontend tests: `cd frontend && pnpm test`
- [ ] Run frontend linter: `cd frontend && pnpm lint`

## Post-Completion

- [ ] Move this plan to `docs/plans/completed/`
