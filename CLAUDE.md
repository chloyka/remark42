# Remark42 Development Guidelines

## Build/Test/Lint Commands
- **Backend**:
  - Run server: `make rundev`
  - Build: `make backend`
  - Race test: `make race_test`
- **Backend Testing**:
  - Run all tests: `cd backend/app && go test -timeout=60s -count 1 ./...`
  - Run single test: `cd backend/app && go test -run TestName ./path/to/package`
  - **PostgreSQL tests**: Require `TEST_POSTGRES_DSN` env var pointing to a running PostgreSQL instance; tests skip automatically if not set
  - **IMPORTANT**: Run example tests: `cd backend/_example/memory_store && go test -race ./... && go build -race ./...`
- **Frontend**:
  - Development: `cd frontend && pnpm dev:app`
  - Tests: `cd frontend && pnpm test`
- **Lint**:
  - Backend: `cd backend && golangci-lint run`
  - **IMPORTANT**: Example lint: `cd backend/_example/memory_store && golangci-lint run --config ../../.golangci.yml`
  - Frontend: `cd frontend && pnpm lint`
  - **Before committing**: Always run tests and linter on both main backend AND examples

## Code Style
- **Backend**: Formatting with golangci-lint, strict error handling
- **Frontend**: TypeScript with ESLint, Stylelint and Prettier
- **Imports**: Group stdlib, external packages, then internal packages
- **CSS**: CSS Modules for new components (`component.module.css`)

## Key Backend Packages
- **Web/API**: `github.com/go-chi/chi/v5`, `github.com/go-pkgz/rest`
- **Auth**: `github.com/go-pkgz/auth/v2`
- **Logging**: `github.com/go-pkgz/lgr`
- **Testing**: `github.com/stretchr/testify`
- **Notifications**: `github.com/go-pkgz/notify`
- **Storage (PostgreSQL)**: `gorm.io/gorm`, `gorm.io/driver/postgres`

## Repository Structure
- Backend: Go server using BoltDB (default) or PostgreSQL (via GORM) for storage
- Frontend: Preact/Redux-based UI with iframe embedding
