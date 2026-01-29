package engine

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/remark42/backend/app/store"
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

// prepPostgres creates a PostgresDB instance with 2 comments pre-loaded and returns a teardown function.
// Mirrors the bolt test prep function.
func prepPostgres(t *testing.T) (pg *PostgresDB, teardown func()) {
	t.Helper()
	dsn := getTestDSN(t)

	pg, err := NewPostgresDB(dsn, []string{"radio-t"})
	require.NoError(t, err)

	// clean all tables before test
	cleanPostgresTables(t, pg)

	comment1 := store.Comment{
		ID:        "id-1",
		Text:      `some text, <a href="http://radio-t.com">link</a>`,
		Timestamp: time.Date(2017, 12, 20, 15, 18, 22, 0, time.UTC),
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		User:      store.User{ID: "user1", Name: "user name"},
	}
	_, err = pg.Create(comment1)
	require.NoError(t, err)

	comment2 := store.Comment{
		ID:        "id-2",
		Text:      "some text2",
		Timestamp: time.Date(2017, 12, 20, 15, 18, 23, 0, time.UTC),
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		User:      store.User{ID: "user1", Name: "user name"},
	}
	_, err = pg.Create(comment2)
	require.NoError(t, err)

	teardown = func() {
		cleanPostgresTables(t, pg)
		require.NoError(t, pg.Close())
	}
	return pg, teardown
}

func cleanPostgresTables(t *testing.T, pg *PostgresDB) {
	t.Helper()
	for _, table := range []string{"comments", "post_info", "blocked_users", "verified_users", "readonly_posts", "user_details"} {
		err := pg.db.Exec("DELETE FROM " + table).Error
		require.NoError(t, err, "failed to clean table %s", table)
	}
}

func TestPostgresDB_CreateAndGet(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// verify both comments exist via Get
	c1, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)
	assert.Equal(t, `some text, <a href="http://radio-t.com">link</a>`, c1.Text)
	assert.Equal(t, "user1", c1.User.ID)
	assert.Equal(t, "user name", c1.User.Name)
	assert.Equal(t, "radio-t", c1.Locator.SiteID)
	assert.Equal(t, "https://radio-t.com", c1.Locator.URL)

	c2, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-2",
	})
	require.NoError(t, err)
	assert.Equal(t, "some text2", c2.Text)

	// duplicate creation should fail
	_, err = pg.Create(store.Comment{
		ID:      "id-1",
		Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key id-1 already in store")
}

func TestPostgresDB_CreateWithPostInfo(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// verify post info was created
	var pi GormPostInfo
	err := pg.db.Where("site_id = ? AND url = ?", "radio-t", "https://radio-t.com").First(&pi).Error
	require.NoError(t, err)
	assert.Equal(t, 2, pi.Count)
	assert.False(t, pi.FirstTS.IsZero())
	assert.False(t, pi.LastTS.IsZero())
	assert.True(t, pi.LastTS.After(pi.FirstTS) || pi.LastTS.Equal(pi.FirstTS))
}

func TestPostgresDB_CreateFailedReadOnly(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// manually set post as read-only
	err := pg.db.Create(&GormReadOnlyPost{
		SiteID: "radio-t",
		URL:    "https://radio-t.com/ro",
	}).Error
	require.NoError(t, err)

	comment := store.Comment{
		ID:        "id-ro",
		Text:      "some text",
		Timestamp: time.Date(2017, 12, 20, 15, 18, 22, 0, time.UTC),
		Locator:   store.Locator{URL: "https://radio-t.com/ro", SiteID: "radio-t"},
		User:      store.User{ID: "user1", Name: "user name"},
	}

	_, err = pg.Create(comment)
	assert.Error(t, err)
	assert.Equal(t, "post https://radio-t.com/ro is read-only", err.Error())

	// remove read-only flag
	err = pg.db.Where("site_id = ? AND url = ?", "radio-t", "https://radio-t.com/ro").Delete(&GormReadOnlyPost{}).Error
	require.NoError(t, err)

	// now creation should succeed
	_, err = pg.Create(comment)
	assert.NoError(t, err)
}

func TestPostgresDB_Get(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// get existing comment
	comment, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-2",
	})
	assert.NoError(t, err)
	assert.Equal(t, "some text2", comment.Text)

	// get non-existent comment
	_, err = pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "1234567",
	})
	assert.Error(t, err)

	// get from non-existent site/url
	_, err = pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "bad"},
		CommentID: "id-2",
	})
	assert.Error(t, err)
}

func TestPostgresDB_Update(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// get original comment
	c, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)

	// update text and score
	c.Text = "abc 123"
	c.Score = 100
	err = pg.Update(c)
	assert.NoError(t, err)

	// verify the update
	updated, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "abc 123", updated.Text)
	assert.Equal(t, "id-1", updated.ID)
	assert.Equal(t, 100, updated.Score)
	// immutable fields should be preserved
	assert.Equal(t, "user1", updated.User.ID)
	assert.Equal(t, "user name", updated.User.Name)

	// update non-existent comment (wrong URL)
	c.Locator.URL = "https://radio-t.com-bad"
	err = pg.Update(c)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestPostgresDB_UpdateWithEdit(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	c, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)

	// add edit info
	editTS := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	c.Edit = &store.Edit{
		Timestamp: editTS,
		Summary:   "fixed typo",
	}
	c.Text = "edited text"
	err = pg.Update(c)
	require.NoError(t, err)

	updated, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "edited text", updated.Text)
	require.NotNil(t, updated.Edit)
	assert.Equal(t, "fixed typo", updated.Edit.Summary)
	assert.True(t, editTS.Equal(updated.Edit.Timestamp))
}

func TestPostgresDB_DeleteComment(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// soft delete
	delReq := DeleteRequest{
		Locator:    store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID:  "id-1",
		DeleteMode: store.SoftDelete,
	}
	err := pg.Delete(delReq)
	assert.NoError(t, err)

	// verify comment is soft deleted
	c, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)
	assert.True(t, c.Deleted)
	assert.Equal(t, "", c.Text)
	assert.Equal(t, "user1", c.User.ID, "user info preserved on soft delete")
	assert.Equal(t, "user name", c.User.Name, "user name preserved on soft delete")

	// repeated deletion should not error
	err = pg.Delete(delReq)
	assert.NoError(t, err)

	// second comment should be unchanged
	c2, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-2",
	})
	require.NoError(t, err)
	assert.Equal(t, "some text2", c2.Text)
	assert.False(t, c2.Deleted)

	// post info should reflect only 1 non-deleted comment
	var pi GormPostInfo
	err = pg.db.Where("site_id = ? AND url = ?", "radio-t", "https://radio-t.com").First(&pi).Error
	require.NoError(t, err)
	assert.Equal(t, 1, pi.Count)

	// delete non-existent comment
	delReq.CommentID = "123456"
	err = pg.Delete(delReq)
	assert.Error(t, err)

	// delete with non-existent URL
	delReq.Locator = store.Locator{URL: "https://radio-t.com/bad", SiteID: "radio-t"}
	delReq.CommentID = "id-1"
	err = pg.Delete(delReq)
	assert.Error(t, err)
}

func TestPostgresDB_DeleteHard(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	delReq := DeleteRequest{
		Locator:    store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID:  "id-1",
		DeleteMode: store.HardDelete,
	}
	err := pg.Delete(delReq)
	assert.NoError(t, err)

	// verify comment is hard deleted (user info replaced with "deleted")
	c, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)
	assert.True(t, c.Deleted)
	assert.Equal(t, "", c.Text)
	assert.Equal(t, "deleted", c.User.ID)
	assert.Equal(t, "deleted", c.User.Name)
	assert.Equal(t, "", c.User.Picture)
	assert.Equal(t, "", c.User.IP)
}

func TestPostgresDB_DeleteAll(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	delReq := DeleteRequest{Locator: store.Locator{SiteID: "radio-t"}}
	err := pg.Delete(delReq)
	assert.NoError(t, err)

	// verify all comments are gone
	_, err = pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	assert.Error(t, err)

	_, err = pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-2",
	})
	assert.Error(t, err)

	// verify post info is gone
	var piCount int64
	pg.db.Model(&GormPostInfo{}).Where("site_id = ?", "radio-t").Count(&piCount)
	assert.Equal(t, int64(0), piCount)
}

func TestPostgresDB_DeleteUserHard(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// first soft delete one comment
	err := pg.Delete(DeleteRequest{
		Locator:    store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID:  "id-1",
		DeleteMode: store.SoftDelete,
	})
	require.NoError(t, err)

	// now hard delete user
	err = pg.Delete(DeleteRequest{
		Locator:    store.Locator{SiteID: "radio-t"},
		UserID:     "user1",
		DeleteMode: store.HardDelete,
	})
	require.NoError(t, err)

	// both comments should still exist but with "deleted" user
	c1, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)
	assert.True(t, c1.Deleted)
	assert.Equal(t, "deleted", c1.User.ID)
	assert.Equal(t, "deleted", c1.User.Name)

	c2, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-2",
	})
	require.NoError(t, err)
	assert.True(t, c2.Deleted)
	assert.Equal(t, "deleted", c2.User.ID)
	assert.Equal(t, "deleted", c2.User.Name)

	// unknown user should return error
	err = pg.Delete(DeleteRequest{
		Locator:    store.Locator{SiteID: "radio-t"},
		UserID:     "unknown-user",
		DeleteMode: store.HardDelete,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown user")
}

func TestPostgresDB_DeleteUserSoft(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// soft delete all user comments
	err := pg.Delete(DeleteRequest{
		Locator:    store.Locator{SiteID: "radio-t"},
		UserID:     "user1",
		DeleteMode: store.SoftDelete,
	})
	require.NoError(t, err)

	// both comments should be deleted but user info preserved
	c1, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-1",
	})
	require.NoError(t, err)
	assert.True(t, c1.Deleted)
	assert.Equal(t, "", c1.Text)
	assert.Equal(t, "user1", c1.User.ID)
	assert.Equal(t, "user name", c1.User.Name)

	c2, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID: "id-2",
	})
	require.NoError(t, err)
	assert.True(t, c2.Deleted)
	assert.Equal(t, "", c2.Text)
	assert.Equal(t, "user1", c2.User.ID)
}

func TestPostgresDB_DeleteUserDetail(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// create a user detail entry manually
	detail := GormUserDetail{
		SiteID: "radio-t",
		UserID: "user1",
		Email:  "test@example.com",
	}
	err := pg.db.Create(&detail).Error
	require.NoError(t, err)

	// delete email detail
	err = pg.Delete(DeleteRequest{
		Locator:    store.Locator{SiteID: "radio-t"},
		UserID:     "user1",
		UserDetail: UserEmail,
	})
	require.NoError(t, err)

	// verify email is cleared and entry is deleted (no other details remain)
	var count int64
	pg.db.Model(&GormUserDetail{}).Where("site_id = ? AND user_id = ?", "radio-t", "user1").Count(&count)
	assert.Equal(t, int64(0), count, "entry should be removed when all details are empty")

	// create entry with both email and telegram
	detail2 := GormUserDetail{
		SiteID:   "radio-t",
		UserID:   "user1",
		Email:    "test@example.com",
		Telegram: "@test",
	}
	err = pg.db.Create(&detail2).Error
	require.NoError(t, err)

	// delete only email
	err = pg.Delete(DeleteRequest{
		Locator:    store.Locator{SiteID: "radio-t"},
		UserID:     "user1",
		UserDetail: UserEmail,
	})
	require.NoError(t, err)

	// verify email is cleared but telegram remains
	var remaining GormUserDetail
	err = pg.db.Where("site_id = ? AND user_id = ?", "radio-t", "user1").First(&remaining).Error
	require.NoError(t, err)
	assert.Equal(t, "", remaining.Email)
	assert.Equal(t, "@test", remaining.Telegram)

	// delete all details
	err = pg.Delete(DeleteRequest{
		Locator:    store.Locator{SiteID: "radio-t"},
		UserID:     "user1",
		UserDetail: AllUserDetails,
	})
	require.NoError(t, err)

	pg.db.Model(&GormUserDetail{}).Where("site_id = ? AND user_id = ?", "radio-t", "user1").Count(&count)
	assert.Equal(t, int64(0), count, "entry should be removed after AllUserDetails delete")
}

func TestPostgresDB_FindForPost(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// find all comments for the post
	res, err := pg.Find(FindRequest{Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"}, Sort: "+time"})
	require.NoError(t, err)
	require.Equal(t, 2, len(res))
	assert.Equal(t, "id-1", res[0].ID)
	assert.Equal(t, "id-2", res[1].ID)

	// reverse sort
	res, err = pg.Find(FindRequest{Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"}, Sort: "-time"})
	require.NoError(t, err)
	require.Equal(t, 2, len(res))
	assert.Equal(t, "id-2", res[0].ID)
	assert.Equal(t, "id-1", res[1].ID)

	// non-existent URL returns empty
	res, err = pg.Find(FindRequest{Locator: store.Locator{URL: "https://bad-url.com", SiteID: "radio-t"}, Sort: "+time"})
	require.NoError(t, err)
	assert.Equal(t, 0, len(res))

	// non-existent site returns empty
	res, err = pg.Find(FindRequest{Locator: store.Locator{URL: "https://radio-t.com", SiteID: "bad-site"}, Sort: "+time"})
	require.NoError(t, err)
	assert.Equal(t, 0, len(res))
}

func TestPostgresDB_FindForPostSince(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// since before both comments - returns both
	since := time.Date(2017, 12, 20, 15, 18, 21, 0, time.UTC)
	res, err := pg.Find(FindRequest{
		Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		Since:   since,
		Sort:    "+time",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, len(res))

	// since between the two comments - returns only the second
	since = time.Date(2017, 12, 20, 15, 18, 22, 0, time.UTC)
	res, err = pg.Find(FindRequest{
		Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		Since:   since,
		Sort:    "+time",
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(res))
	assert.Equal(t, "id-2", res[0].ID)

	// since after both comments - returns none
	since = time.Date(2017, 12, 20, 15, 18, 24, 0, time.UTC)
	res, err = pg.Find(FindRequest{
		Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		Since:   since,
		Sort:    "+time",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, len(res))
}

func TestPostgresDB_FindLastForSite(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// add a comment on a different post
	c3 := store.Comment{
		ID:        "id-3",
		Text:      "third comment",
		Timestamp: time.Date(2017, 12, 20, 15, 18, 25, 0, time.UTC),
		Locator:   store.Locator{URL: "https://radio-t.com/post2", SiteID: "radio-t"},
		User:      store.User{ID: "user2", Name: "other user"},
	}
	_, err := pg.Create(c3)
	require.NoError(t, err)

	// find last for site (no URL, no UserID), sorted by -time (default for site-wide)
	res, err := pg.Find(FindRequest{Locator: store.Locator{SiteID: "radio-t"}, Sort: "-time"})
	require.NoError(t, err)
	require.Equal(t, 3, len(res))
	assert.Equal(t, "id-3", res[0].ID) // most recent first
	assert.Equal(t, "id-2", res[1].ID)
	assert.Equal(t, "id-1", res[2].ID)

	// with limit
	res, err = pg.Find(FindRequest{Locator: store.Locator{SiteID: "radio-t"}, Limit: 2, Sort: "-time"})
	require.NoError(t, err)
	assert.Equal(t, 2, len(res))
	assert.Equal(t, "id-3", res[0].ID)
	assert.Equal(t, "id-2", res[1].ID)

	// with since filter
	since := time.Date(2017, 12, 20, 15, 18, 23, 0, time.UTC)
	res, err = pg.Find(FindRequest{Locator: store.Locator{SiteID: "radio-t"}, Since: since, Sort: "-time"})
	require.NoError(t, err)
	require.Equal(t, 1, len(res))
	assert.Equal(t, "id-3", res[0].ID)

	// deleted comments should be excluded from site-wide find
	err = pg.Delete(DeleteRequest{
		Locator:    store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID:  "id-1",
		DeleteMode: store.SoftDelete,
	})
	require.NoError(t, err)

	res, err = pg.Find(FindRequest{Locator: store.Locator{SiteID: "radio-t"}, Sort: "-time"})
	require.NoError(t, err)
	assert.Equal(t, 2, len(res))
}

func TestPostgresDB_FindForUser(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// add more comments from a different user
	c3 := store.Comment{
		ID:        "id-3",
		Text:      "user2 comment",
		Timestamp: time.Date(2017, 12, 20, 15, 18, 25, 0, time.UTC),
		Locator:   store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		User:      store.User{ID: "user2", Name: "another user"},
	}
	_, err := pg.Create(c3)
	require.NoError(t, err)

	// find comments for user1
	res, err := pg.Find(FindRequest{
		Locator: store.Locator{SiteID: "radio-t"},
		UserID:  "user1",
		Sort:    "+time",
	})
	require.NoError(t, err)
	require.Equal(t, 2, len(res))
	assert.Equal(t, "id-1", res[0].ID)
	assert.Equal(t, "id-2", res[1].ID)

	// find comments for user2
	res, err = pg.Find(FindRequest{
		Locator: store.Locator{SiteID: "radio-t"},
		UserID:  "user2",
		Sort:    "+time",
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(res))
	assert.Equal(t, "id-3", res[0].ID)

	// non-existent user returns empty
	res, err = pg.Find(FindRequest{
		Locator: store.Locator{SiteID: "radio-t"},
		UserID:  "no-such-user",
		Sort:    "+time",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, len(res))
}

func TestPostgresDB_FindForUserPagination(t *testing.T) {
	dsn := getTestDSN(t)
	pg, err := NewPostgresDB(dsn, []string{"test-site"})
	require.NoError(t, err)
	cleanPostgresTables(t, pg)
	defer func() {
		cleanPostgresTables(t, pg)
		require.NoError(t, pg.Close())
	}()

	// create 10 comments
	for i := 0; i < 10; i++ {
		c := store.Comment{
			ID:        fmt.Sprintf("id-%d", i),
			Text:      fmt.Sprintf("text %d", i),
			Timestamp: time.Date(2017, 12, 20, 15, 18, 22+i, 0, time.UTC),
			Locator:   store.Locator{URL: "https://example.com", SiteID: "test-site"},
			User:      store.User{ID: "user1", Name: "test"},
		}
		_, err = pg.Create(c)
		require.NoError(t, err)
	}

	// find with limit 3, skip 0
	res, err := pg.Find(FindRequest{
		Locator: store.Locator{SiteID: "test-site"},
		UserID:  "user1",
		Limit:   3,
		Sort:    "-time",
	})
	require.NoError(t, err)
	require.Equal(t, 3, len(res))
	assert.Equal(t, "id-9", res[0].ID)
	assert.Equal(t, "id-8", res[1].ID)
	assert.Equal(t, "id-7", res[2].ID)

	// find with limit 3, skip 3
	res, err = pg.Find(FindRequest{
		Locator: store.Locator{SiteID: "test-site"},
		UserID:  "user1",
		Limit:   3,
		Skip:    3,
		Sort:    "-time",
	})
	require.NoError(t, err)
	require.Equal(t, 3, len(res))
	assert.Equal(t, "id-6", res[0].ID)
	assert.Equal(t, "id-5", res[1].ID)
	assert.Equal(t, "id-4", res[2].ID)

	// skip beyond total
	res, err = pg.Find(FindRequest{
		Locator: store.Locator{SiteID: "test-site"},
		UserID:  "user1",
		Limit:   3,
		Skip:    20,
		Sort:    "-time",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, len(res))
}

func TestPostgresDB_FindSortByScore(t *testing.T) {
	dsn := getTestDSN(t)
	pg, err := NewPostgresDB(dsn, []string{"test-site"})
	require.NoError(t, err)
	cleanPostgresTables(t, pg)
	defer func() {
		cleanPostgresTables(t, pg)
		require.NoError(t, pg.Close())
	}()

	comments := []store.Comment{
		{ID: "s1", Text: "low score", Score: 1, Timestamp: time.Date(2017, 12, 20, 15, 18, 22, 0, time.UTC),
			Locator: store.Locator{URL: "https://example.com", SiteID: "test-site"}, User: store.User{ID: "u1", Name: "t"}},
		{ID: "s2", Text: "high score", Score: 10, Timestamp: time.Date(2017, 12, 20, 15, 18, 23, 0, time.UTC),
			Locator: store.Locator{URL: "https://example.com", SiteID: "test-site"}, User: store.User{ID: "u1", Name: "t"}},
		{ID: "s3", Text: "mid score", Score: 5, Timestamp: time.Date(2017, 12, 20, 15, 18, 24, 0, time.UTC),
			Locator: store.Locator{URL: "https://example.com", SiteID: "test-site"}, User: store.User{ID: "u1", Name: "t"}},
	}
	for _, c := range comments {
		_, err = pg.Create(c)
		require.NoError(t, err)
	}

	// sort by +score (ascending)
	res, err := pg.Find(FindRequest{
		Locator: store.Locator{URL: "https://example.com", SiteID: "test-site"},
		Sort:    "+score",
	})
	require.NoError(t, err)
	require.Equal(t, 3, len(res))
	assert.Equal(t, "s1", res[0].ID) // score 1
	assert.Equal(t, "s3", res[1].ID) // score 5
	assert.Equal(t, "s2", res[2].ID) // score 10

	// sort by -score (descending)
	res, err = pg.Find(FindRequest{
		Locator: store.Locator{URL: "https://example.com", SiteID: "test-site"},
		Sort:    "-score",
	})
	require.NoError(t, err)
	require.Equal(t, 3, len(res))
	assert.Equal(t, "s2", res[0].ID) // score 10
	assert.Equal(t, "s3", res[1].ID) // score 5
	assert.Equal(t, "s1", res[2].ID) // score 1
}

func TestPostgresDB_CountPost(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// count for existing post
	count, err := pg.Count(FindRequest{Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"}})
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// count for non-existent URL
	count, err = pg.Count(FindRequest{Locator: store.Locator{URL: "https://no-such-url.com", SiteID: "radio-t"}})
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// count after soft delete
	err = pg.Delete(DeleteRequest{
		Locator:    store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		CommentID:  "id-1",
		DeleteMode: store.SoftDelete,
	})
	require.NoError(t, err)
	count, err = pg.Count(FindRequest{Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"}})
	require.NoError(t, err)
	assert.Equal(t, 1, count, "soft deleted comment should not be counted")
}

func TestPostgresDB_CountUser(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// count for existing user
	count, err := pg.Count(FindRequest{Locator: store.Locator{SiteID: "radio-t"}, UserID: "user1"})
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// count for non-existent user
	count, err = pg.Count(FindRequest{Locator: store.Locator{SiteID: "radio-t"}, UserID: "no-user"})
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// invalid request
	_, err = pg.Count(FindRequest{Locator: store.Locator{SiteID: "radio-t"}})
	assert.Error(t, err)
}

func TestPostgresDB_InfoPost(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// get info for existing post
	infos, err := pg.Info(InfoRequest{Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"}})
	require.NoError(t, err)
	require.Equal(t, 1, len(infos))
	assert.Equal(t, "https://radio-t.com", infos[0].URL)
	assert.Equal(t, 2, infos[0].Count)
	assert.False(t, infos[0].ReadOnly)
	assert.False(t, infos[0].FirstTS.IsZero())
	assert.False(t, infos[0].LastTS.IsZero())

	// info for non-existent post
	_, err = pg.Info(InfoRequest{Locator: store.Locator{URL: "https://bad-url.com", SiteID: "radio-t"}})
	assert.Error(t, err)
}

func TestPostgresDB_InfoPostReadOnly(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// test ReadOnlyAge: posts from 2017 should be read-only with a small ReadOnlyAge
	infos, err := pg.Info(InfoRequest{
		Locator:     store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
		ReadOnlyAge: 1, // 1 day — old posts should be read-only
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(infos))
	assert.True(t, infos[0].ReadOnly, "old post should be read-only with ReadOnlyAge=1")

	// without ReadOnlyAge, should not be read-only
	infos, err = pg.Info(InfoRequest{
		Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(infos))
	assert.False(t, infos[0].ReadOnly)

	// manually set read-only flag
	err = pg.db.Create(&GormReadOnlyPost{SiteID: "radio-t", URL: "https://radio-t.com"}).Error
	require.NoError(t, err)

	infos, err = pg.Info(InfoRequest{
		Locator: store.Locator{URL: "https://radio-t.com", SiteID: "radio-t"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, len(infos))
	assert.True(t, infos[0].ReadOnly, "manually flagged post should be read-only")
}

func TestPostgresDB_InfoList(t *testing.T) {
	pg, teardown := prepPostgres(t)
	defer teardown()

	// add comments to a second post
	c3 := store.Comment{
		ID:        "id-3",
		Text:      "post2 comment",
		Timestamp: time.Date(2017, 12, 21, 10, 0, 0, 0, time.UTC),
		Locator:   store.Locator{URL: "https://radio-t.com/post2", SiteID: "radio-t"},
		User:      store.User{ID: "user2", Name: "another"},
	}
	_, err := pg.Create(c3)
	require.NoError(t, err)

	// list all post infos for site
	infos, err := pg.Info(InfoRequest{Locator: store.Locator{SiteID: "radio-t"}})
	require.NoError(t, err)
	assert.Equal(t, 2, len(infos))

	// with limit
	infos, err = pg.Info(InfoRequest{Locator: store.Locator{SiteID: "radio-t"}, Limit: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, len(infos))

	// with skip
	infos, err = pg.Info(InfoRequest{Locator: store.Locator{SiteID: "radio-t"}, Skip: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, len(infos))

	// skip beyond total
	infos, err = pg.Info(InfoRequest{Locator: store.Locator{SiteID: "radio-t"}, Skip: 10})
	require.NoError(t, err)
	assert.Equal(t, 0, len(infos))

	// non-existent site
	infos, err = pg.Info(InfoRequest{Locator: store.Locator{SiteID: "bad-site"}})
	require.NoError(t, err)
	assert.Equal(t, 0, len(infos))
}

func TestPostgresDB_CreateWithVotesAndEdit(t *testing.T) {
	dsn := getTestDSN(t)
	pg, err := NewPostgresDB(dsn, []string{"test-site"})
	require.NoError(t, err)
	cleanPostgresTables(t, pg)
	defer func() {
		cleanPostgresTables(t, pg)
		require.NoError(t, pg.Close())
	}()

	editTS := time.Date(2018, 1, 1, 0, 0, 0, 0, time.UTC)
	comment := store.Comment{
		ID:        "id-votes",
		Text:      "voted comment",
		Timestamp: time.Date(2017, 12, 20, 15, 18, 22, 0, time.UTC),
		Locator:   store.Locator{URL: "https://example.com", SiteID: "test-site"},
		User:      store.User{ID: "u1", Name: "voter"},
		Score:     5,
		Votes:     map[string]bool{"u2": true, "u3": false},
		VotedIPs: map[string]store.VotedIPInfo{
			"ip1": {Timestamp: time.Date(2017, 12, 20, 15, 18, 22, 0, time.UTC), Value: true},
		},
		Edit: &store.Edit{
			Timestamp: editTS,
			Summary:   "initial edit",
		},
		Pin:      true,
		Imported: true,
	}

	_, err = pg.Create(comment)
	require.NoError(t, err)

	// get and verify round-trip
	got, err := pg.Get(GetRequest{
		Locator:   store.Locator{URL: "https://example.com", SiteID: "test-site"},
		CommentID: "id-votes",
	})
	require.NoError(t, err)
	assert.Equal(t, "voted comment", got.Text)
	assert.Equal(t, 5, got.Score)
	assert.Equal(t, true, got.Votes["u2"])
	assert.Equal(t, false, got.Votes["u3"])
	require.NotNil(t, got.VotedIPs["ip1"])
	assert.Equal(t, true, got.VotedIPs["ip1"].Value)
	require.NotNil(t, got.Edit)
	assert.Equal(t, "initial edit", got.Edit.Summary)
	assert.True(t, got.Pin)
	assert.True(t, got.Imported)
}
