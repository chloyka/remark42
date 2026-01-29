package engine

import (
	"fmt"

	log "github.com/go-pkgz/lgr"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/umputun/remark42/backend/app/store"
)

// PostgresDB implements engine.Interface using PostgreSQL via GORM
type PostgresDB struct {
	db *gorm.DB
}

// NewPostgresDB creates a new PostgreSQL-backed storage engine.
// It opens a GORM connection, runs AutoMigrate for all tables, and verifies connectivity.
func NewPostgresDB(dsn string, sites []string) (*PostgresDB, error) {
	log.Printf("[INFO] postgres store, sites %v", sites)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres connection: %w", err)
	}

	// run auto-migration for all GORM models
	if err = db.AutoMigrate(
		&GormComment{},
		&GormPostInfo{},
		&GormBlockedUser{},
		&GormVerifiedUser{},
		&GormReadOnlyPost{},
		&GormUserDetail{},
	); err != nil {
		return nil, fmt.Errorf("failed to auto-migrate postgres tables: %w", err)
	}

	// verify connectivity
	var result int
	if err = db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		return nil, fmt.Errorf("failed to verify postgres connectivity: %w", err)
	}

	log.Printf("[INFO] postgres store initialized, AutoMigrate completed")
	return &PostgresDB{db: db}, nil
}

// Close closes the underlying database connection
func (p *PostgresDB) Close() error {
	sqlDB, err := p.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}
	if err = sqlDB.Close(); err != nil {
		return fmt.Errorf("failed to close postgres connection: %w", err)
	}
	return nil
}

// placeholder methods to partially satisfy engine.Interface — will be implemented in later tasks

// Create adds a new comment
func (p *PostgresDB) Create(_ store.Comment) (string, error) {
	return "", fmt.Errorf("not implemented")
}

// Update modifies an existing comment
func (p *PostgresDB) Update(_ store.Comment) error {
	return fmt.Errorf("not implemented")
}

// Get retrieves a comment by ID
func (p *PostgresDB) Get(_ GetRequest) (store.Comment, error) {
	return store.Comment{}, fmt.Errorf("not implemented")
}

// Find returns comments matching the request
func (p *PostgresDB) Find(_ FindRequest) ([]store.Comment, error) {
	return nil, fmt.Errorf("not implemented")
}

// Info returns post info
func (p *PostgresDB) Info(_ InfoRequest) ([]store.PostInfo, error) {
	return nil, fmt.Errorf("not implemented")
}

// Count returns comment count
func (p *PostgresDB) Count(_ FindRequest) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

// Delete removes comments or related data
func (p *PostgresDB) Delete(_ DeleteRequest) error {
	return fmt.Errorf("not implemented")
}

// Flag gets or sets flag values
func (p *PostgresDB) Flag(_ FlagRequest) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

// ListFlags returns list of flagged entries
func (p *PostgresDB) ListFlags(_ FlagRequest) ([]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

// UserDetail gets or sets user detail values
func (p *PostgresDB) UserDetail(_ UserDetailRequest) ([]UserDetailEntry, error) {
	return nil, fmt.Errorf("not implemented")
}
