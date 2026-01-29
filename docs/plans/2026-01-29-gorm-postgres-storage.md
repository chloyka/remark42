# GORM/PostgreSQL Storage Engine for Remark42

Implement a new storage engine backed by PostgreSQL via GORM, as an alternative to BoltDB. Add `STORAGE_TYPE` and `POSTGRES_DSN` environment variables. On startup, run GORM AutoMigrate; if migration succeeds, start the server. Use GORM models for all CRUD operations.

## Context

- Files involved:
  - `backend/app/store/engine/engine.go` — `engine.Interface` definition (read-only reference)
  - `backend/app/store/engine/bolt.go` — BoltDB implementation (reference for behavior)
  - `backend/app/store/engine/bolt_test.go` — BoltDB tests (reference for test patterns)
  - `backend/app/store/comment.go` — `store.Comment`, `store.Locator`, `store.PostInfo` types
  - `backend/app/store/user.go` — `store.User` type
  - `backend/app/cmd/server.go` — `StoreGroup`, `makeDataStore()`, `ServerCommand`
  - `backend/go.mod` / `backend/go.sum` — dependencies
- Related patterns: BoltDB engine implements `engine.Interface` with per-site multiplexing; `makeDataStore()` switches on `Store.Type`; uses `go-flags` struct tags for env/CLI parsing
- Dependencies: `gorm.io/gorm`, `gorm.io/driver/postgres`

## Approach

- **Testing approach**: Regular (code first, then tests). Tests will use a real PostgreSQL instance (from env `TEST_POSTGRES_DSN`) with `t.Skip` if not available.
- Complete each task fully before moving to the next
- GORM models will be separate structs from `store.Comment`/`store.User` — they map to relational tables and convert to/from domain types
- Multi-site support: comments, posts, flags, etc. all include a `site_id` column (same as BoltDB's per-site-db approach but in one database)
- **CRITICAL: every task MUST include new/updated tests**
- **CRITICAL: all tests must pass before starting next task**

## Task 1: Add GORM dependencies and create GORM models

**Files:**
- Modify: `backend/go.mod`
- Create: `backend/app/store/engine/postgres_models.go`

- [x] Run `go get gorm.io/gorm gorm.io/driver/postgres` in `backend/`
- [x] Create `postgres_models.go` with GORM model structs:
  - `GormComment` — maps to `comments` table. Fields: `ID` (string, PK), `ParentID`, `Text`, `Orig`, `UserID`, `UserName`, `UserPicture`, `UserIP`, `UserAdmin` (bool), `SiteID`, `URL`, `Score` (int), `Votes` (JSON text), `VotedIPs` (JSON text), `Vote` (int), `Controversy` (float64), `Timestamp` (time.Time, indexed), `EditTimestamp` (*time.Time), `EditSummary`, `Pin` (bool), `Deleted` (bool), `Imported` (bool), `PostTitle`. Composite index on `(site_id, url)`, index on `(site_id, user_id)`, index on `(site_id, timestamp)`
  - `GormPostInfo` — maps to `post_info` table. Fields: `URL` (string, part of composite PK with SiteID), `SiteID`, `Count` (int), `ReadOnly` (bool), `FirstTS`, `LastTS`
  - `GormBlockedUser` — maps to `blocked_users` table. Fields: `SiteID`, `UserID`, `Until` (time.Time). Composite PK `(site_id, user_id)`
  - `GormVerifiedUser` — maps to `verified_users` table. Fields: `SiteID`, `UserID`. Composite PK `(site_id, user_id)`
  - `GormReadOnlyPost` — maps to `readonly_posts` table. Fields: `SiteID`, `URL`. Composite PK `(site_id, url)`
  - `GormUserDetail` — maps to `user_details` table. Fields: `SiteID`, `UserID`, `Email`, `Telegram`. Composite PK `(site_id, user_id)`
- [x] Add conversion methods: `ToComment() store.Comment`, `FromComment(store.Comment) GormComment`, similarly for other models
- [x] Write unit tests for model conversions in `backend/app/store/engine/postgres_models_test.go` — test round-trip conversion (Comment -> GormComment -> Comment)
- [x] Run `cd backend/app/store/engine && go test -run TestGormModel -count 1 ./...` — must pass before task 2

## Task 2: Implement PostgresDB engine struct with NewPostgresDB and AutoMigrate

**Files:**
- Create: `backend/app/store/engine/postgres.go`

- [x] Create `PostgresDB` struct with `db *gorm.DB` field
- [x] Implement `NewPostgresDB(dsn string, sites []string) (*PostgresDB, error)`:
  - Open GORM connection with `gorm.Open(postgres.Open(dsn), &gorm.Config{})`
  - Run `db.AutoMigrate(...)` for all GORM models
  - Verify connectivity with `db.Raw("SELECT 1").Scan(...)`
  - Return `*PostgresDB`
- [x] Implement `Close() error` — get underlying `*sql.DB` and close it
- [x] Write test `TestPostgresDB_NewAndClose` — connects to test DB, verifies AutoMigrate runs, closes. Skip if no `TEST_POSTGRES_DSN`
- [x] Run `cd backend/app/store/engine && go test -run TestPostgresDB_New -count 1 ./...` — must pass before task 3

## Task 3: Implement Create, Get, Update, Delete methods

**Files:**
- Modify: `backend/app/store/engine/postgres.go`

- [x] Implement `Create(comment store.Comment) (string, error)`:
  - Convert to `GormComment`, insert with `db.Create()`
  - Check read-only status before creating
  - Update post info (count, timestamps) — upsert `GormPostInfo`
  - Return comment ID
- [x] Implement `Get(req GetRequest) (store.Comment, error)`:
  - Query by `id`, `site_id`, `url`
  - Convert back to `store.Comment`
- [x] Implement `Update(comment store.Comment) error`:
  - Update mutable fields only: `Text`, `Orig`, `Score`, `Votes`, `VotedIPs`, `Vote`, `Controversy`, `Pin`, `Deleted`, `EditTimestamp`, `EditSummary`, `PostTitle`
- [x] Implement `Delete(req DeleteRequest) error`:
  - Handle all delete modes: single comment soft/hard delete, delete all user comments, delete user detail, delete all site data
  - For comment delete: apply `SetDeleted()` logic then update
  - For site delete: delete all comments, post_info, flags, user_details for the site
  - For user delete: soft/hard delete all user comments for site
  - For user detail delete: remove user_detail entry
- [x] Write tests for Create, Get, Update, Delete — mirror `TestBoltDB_CreateAndFind`, `TestBoltDB_Get`, `TestBoltDB_Update`, `TestBoltDB_Delete` patterns
- [x] Run `cd backend/app/store/engine && go test -run TestPostgresDB -count 1 ./...` — must pass before task 4

## Task 4: Implement Find, Count, Info methods

**Files:**
- Modify: `backend/app/store/engine/postgres.go`

- [x] Implement `Find(req FindRequest) ([]store.Comment, error)`:
  - If `req.Locator.URL` is set: find comments for that post
  - If `req.UserID` is set: find comments by user across site
  - If only `req.Locator.SiteID` is set (no URL, no UserID): find last N comments across site
  - Apply `Sort`, `Since`, `Limit`, `Skip`
  - Sort mapping: `+time`/`-time` -> `timestamp ASC/DESC`, `+score`/`-score` -> `score ASC/DESC`, `+controversy`/`-controversy` -> `controversy ASC/DESC`
- [x] Implement `Count(req FindRequest) (int, error)`:
  - Count comments matching the find criteria
- [x] Implement `Info(req InfoRequest) ([]store.PostInfo, error)`:
  - If URL is set: return single post info
  - If only SiteID: return all post infos for site with `Limit`/`Skip`
  - Apply `ReadOnlyAge` if set (posts older than N days become read-only)
- [x] Write tests for Find (by post, by user, by site, with sort/limit/skip), Count, Info
- [x] Run `cd backend/app/store/engine && go test -run TestPostgresDB -count 1 ./...` — must pass before task 5

## Task 5: Implement Flag, ListFlags, UserDetail methods

**Files:**
- Modify: `backend/app/store/engine/postgres.go`

- [ ] Implement `Flag(req FlagRequest) (bool, error)`:
  - `ReadOnly` flag: get/set post read-only status in `readonly_posts` table
  - `Verified` flag: get/set user verified status in `verified_users` table
  - `Blocked` flag: get/set user blocked status in `blocked_users` table (with TTL via `Until` field)
  - When `Update == FlagNonSet`: return current flag value (get mode)
  - When `Update == FlagTrue/FlagFalse`: set the value (set mode)
- [ ] Implement `ListFlags(req FlagRequest) ([]interface{}, error)`:
  - `ReadOnly`: return list of read-only post URLs for site
  - `Verified`: return list of verified user IDs for site
  - `Blocked`: return list of `store.BlockedUser` for site (filter out expired blocks)
- [ ] Implement `UserDetail(req UserDetailRequest) ([]UserDetailEntry, error)`:
  - Get/set user email, telegram details
  - If `Update` is set: upsert the detail value
  - If `Detail == AllUserDetails` and no `UserID`: list all user details for site
  - Otherwise: return detail(s) for specific user
- [ ] Write tests for Flag (all three types, get and set), ListFlags, UserDetail (get, set, list)
- [ ] Run `cd backend/app/store/engine && go test -run TestPostgresDB -count 1 ./...` — must pass before task 6

## Task 6: Wire up PostgresDB in server configuration

**Files:**
- Modify: `backend/app/cmd/server.go`

- [ ] Add `"postgres"` as a choice in `StoreGroup.Type` field: `choice:"bolt" choice:"rpc" choice:"postgres"`
- [ ] Add `Postgres` sub-struct in `StoreGroup`:
  ```
  Postgres struct {
      DSN string `long:"dsn" env:"DSN" description:"PostgreSQL connection string"`
  } `group:"postgres" namespace:"postgres" env-namespace:"POSTGRES"`
  ```
  This gives env var `STORE_POSTGRES_DSN` (matching the `STORE` namespace + `POSTGRES` namespace + `DSN`)
- [ ] Add `"postgres"` case in `makeDataStore()`:
  - Call `engine.NewPostgresDB(s.Store.Postgres.DSN, s.Sites)`
  - Return the engine
- [ ] Add `gorm.io/gorm` and `gorm.io/driver/postgres` imports
- [ ] Write test for postgres case in `makeDataStore` (can verify it returns error with invalid DSN, skip full integration if no DB)
- [ ] Run `cd backend/app && go test -run TestServerCommand -count 1 ./cmd/` — must pass before task 7

## Task 7: Full integration test and cleanup

**Files:**
- Create: `backend/app/store/engine/postgres_test.go` (if not already created in earlier tasks; consolidate all postgres tests here)

- [ ] Write a full integration test `TestPostgresDB_FullCycle` that exercises Create -> Find -> Update -> Delete -> Flag -> ListFlags -> UserDetail -> Info -> Count -> Close in sequence
- [ ] Ensure all existing bolt tests still pass: `cd backend/app/store/engine && go test -count 1 ./...`
- [ ] Run full backend test suite: `cd backend/app && go test -timeout=120s -count 1 ./...`
- [ ] Run linter: `cd backend && golangci-lint run`
- [ ] Run example tests: `cd backend/_example/memory_store && go test -race ./... && go build -race ./...`
- [ ] Run example lint: `cd backend/_example/memory_store && golangci-lint run --config ../../.golangci.yml`

## Verification

- [ ] Manual test: start server with `STORE_TYPE=postgres STORE_POSTGRES_DSN="host=localhost user=remark42 password=test dbname=remark42 sslmode=disable"` and verify it starts without errors, creates tables
- [ ] Run full test suite: `cd backend/app && go test -timeout=120s -count 1 ./...`
- [ ] Run linter: `cd backend && golangci-lint run`
- [ ] Verify test coverage meets 80%+ for new `postgres.go` and `postgres_models.go`

## Post-Completion

- [ ] Update README.md if user-facing changes (document new env vars `STORE_TYPE=postgres`, `STORE_POSTGRES_DSN`)
- [ ] Move this plan to `docs/plans/completed/`

## Notes

- The user mentioned `STORAGE_TYPE` and `POSTGRES_DSN` as env var names. Following the existing project convention with `go-flags` namespacing, the actual env vars will be `STORE_TYPE` (already exists, adding `postgres` option) and `STORE_POSTGRES_DSN`. This is consistent with how existing store config works (`STORE_TYPE`, `STORE_BOLT_PATH`, etc.).
- Postgres tests require a running PostgreSQL instance. Tests should skip gracefully when `TEST_POSTGRES_DSN` env var is not set.
- GORM models are intentionally separate from `store.Comment` to maintain clean relational schema (no nested JSON for the main fields, JSON only for map types like Votes/VotedIPs).
