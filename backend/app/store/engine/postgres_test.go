package engine

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping postgres tests")
	}
	return dsn
}

func TestPostgresDB_NewAndClose(t *testing.T) {
	dsn := getTestDSN(t)

	// test successful creation
	pg, err := NewPostgresDB(dsn, []string{"test-site"})
	require.NoError(t, err)
	require.NotNil(t, pg)
	require.NotNil(t, pg.db)

	// verify we can query
	var result int
	err = pg.db.Raw("SELECT 1").Scan(&result).Error
	assert.NoError(t, err)
	assert.Equal(t, 1, result)

	// verify tables were created by AutoMigrate
	var tables []string
	err = pg.db.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = 'public'").Scan(&tables).Error
	require.NoError(t, err)
	expectedTables := []string{"comments", "post_info", "blocked_users", "verified_users", "readonly_posts", "user_details"}
	for _, expected := range expectedTables {
		assert.Contains(t, tables, expected, "table %s should exist after AutoMigrate", expected)
	}

	// test close
	err = pg.Close()
	assert.NoError(t, err)

	// verify connection is closed — querying should fail
	err = pg.db.Raw("SELECT 1").Scan(&result).Error
	assert.Error(t, err, "should fail after close")
}

func TestPostgresDB_NewInvalidDSN(t *testing.T) {
	// test with invalid DSN — should fail to connect
	pg, err := NewPostgresDB("host=invalid-host-that-does-not-exist port=5432 dbname=nonexistent sslmode=disable connect_timeout=1", []string{"test-site"})
	assert.Error(t, err)
	assert.Nil(t, pg)
}

func TestPostgresDB_InterfaceCompliance(_ *testing.T) {
	// compile-time check that PostgresDB implements engine.Interface
	var _ Interface = (*PostgresDB)(nil)
}
