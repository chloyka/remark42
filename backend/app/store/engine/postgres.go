package engine

import (
	"fmt"
	"time"

	log "github.com/go-pkgz/lgr"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

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

// Create adds a new comment to the store.
// It checks read-only status, rejects duplicates, inserts the comment, and upserts post info.
func (p *PostgresDB) Create(comment store.Comment) (string, error) {
	// check if the post is read-only
	if p.isReadOnly(comment.Locator) {
		return "", fmt.Errorf("post %s is read-only", comment.Locator.URL)
	}

	// check for duplicate
	var count int64
	if err := p.db.Model(&GormComment{}).Where("id = ? AND site_id = ? AND url = ?",
		comment.ID, comment.Locator.SiteID, comment.Locator.URL).Count(&count).Error; err != nil {
		return "", fmt.Errorf("failed to check for duplicate: %w", err)
	}
	if count > 0 {
		return "", fmt.Errorf("key %s already in store", comment.ID)
	}

	gc := FromComment(comment)
	if err := p.db.Create(&gc).Error; err != nil {
		return "", fmt.Errorf("failed to create comment %s: %w", comment.ID, err)
	}

	// upsert post info
	if err := p.upsertPostInfo(comment); err != nil {
		log.Printf("[WARN] failed to upsert post info for %s: %v", comment.Locator.URL, err)
	}

	return comment.ID, nil
}

// upsertPostInfo updates or creates post_info entry for the comment's post
func (p *PostgresDB) upsertPostInfo(comment store.Comment) error {
	// count non-deleted comments for this post
	var cnt int64
	if err := p.db.Model(&GormComment{}).
		Where("site_id = ? AND url = ? AND deleted = ?", comment.Locator.SiteID, comment.Locator.URL, false).
		Count(&cnt).Error; err != nil {
		return fmt.Errorf("failed to count comments for post info: %w", err)
	}

	// get first and last timestamps
	var firstTS, lastTS time.Time
	row := p.db.Model(&GormComment{}).
		Where("site_id = ? AND url = ?", comment.Locator.SiteID, comment.Locator.URL).
		Select("MIN(timestamp), MAX(timestamp)").Row()
	if err := row.Scan(&firstTS, &lastTS); err != nil {
		return fmt.Errorf("failed to get timestamps for post info: %w", err)
	}

	pi := GormPostInfo{
		URL:     comment.Locator.URL,
		SiteID:  comment.Locator.SiteID,
		Count:   int(cnt),
		FirstTS: firstTS,
		LastTS:  lastTS,
	}

	// upsert: on conflict update count and timestamps
	if err := p.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "url"}, {Name: "site_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"count", "first_ts", "last_ts"}),
	}).Create(&pi).Error; err != nil {
		return fmt.Errorf("failed to upsert post info: %w", err)
	}

	return nil
}

// Get returns a comment by ID, site, and URL
func (p *PostgresDB) Get(req GetRequest) (store.Comment, error) {
	var gc GormComment
	result := p.db.Where("id = ? AND site_id = ? AND url = ?",
		req.CommentID, req.Locator.SiteID, req.Locator.URL).First(&gc)
	if result.Error != nil {
		return store.Comment{}, fmt.Errorf("comment %s not found for %s in store: %w",
			req.CommentID, req.Locator.URL, result.Error)
	}
	return gc.ToComment(), nil
}

// Update modifies mutable fields of an existing comment.
// Immutable fields (ParentID, Locator, Timestamp, User) are preserved from the existing record.
func (p *PostgresDB) Update(comment store.Comment) error {
	// get existing comment to preserve immutable fields
	existing, err := p.Get(GetRequest{Locator: comment.Locator, CommentID: comment.ID})
	if err == nil {
		comment.ParentID = existing.ParentID
		comment.Locator = existing.Locator
		comment.Timestamp = existing.Timestamp
		comment.User = existing.User
	}

	gc := FromComment(comment)
	result := p.db.Where("id = ? AND site_id = ? AND url = ?",
		comment.ID, comment.Locator.SiteID, comment.Locator.URL).
		Select("text", "orig", "score", "votes", "voted_ips", "vote", "controversy",
			"pin", "deleted", "edit_timestamp", "edit_summary", "post_title").
		Updates(&gc)
	if result.Error != nil {
		return fmt.Errorf("failed to update comment %s: %w", comment.ID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("comment %s not found for %s in store", comment.ID, comment.Locator.URL)
	}
	return nil
}

// Delete removes comments or related data based on the request.
// Supports: single comment (soft/hard), all user comments, user detail deletion, and full site deletion.
func (p *PostgresDB) Delete(req DeleteRequest) error {
	switch {
	case req.UserDetail != "": // delete user detail
		return p.deleteUserDetail(req.Locator.SiteID, req.UserID, req.UserDetail)
	case req.Locator.URL != "" && req.CommentID != "" && req.UserDetail == "": // delete comment
		return p.deleteComment(req.Locator, req.CommentID, req.DeleteMode)
	case req.Locator.SiteID != "" && req.UserID != "" && req.CommentID == "" && req.UserDetail == "": // delete user
		return p.deleteUser(req.Locator.SiteID, req.UserID, req.DeleteMode)
	case req.Locator.SiteID != "" && req.Locator.URL == "" && req.CommentID == "" && req.UserID == "" && req.UserDetail == "": // delete site
		return p.deleteAll(req.Locator.SiteID)
	}
	return fmt.Errorf("invalid delete request %+v", req)
}

// deleteComment soft or hard deletes a single comment
func (p *PostgresDB) deleteComment(locator store.Locator, commentID string, mode store.DeleteMode) error {
	// load existing comment
	existing, err := p.Get(GetRequest{Locator: locator, CommentID: commentID})
	if err != nil {
		return fmt.Errorf("can't load key %s from bucket %s: %w", commentID, locator.URL, err)
	}

	wasDeleted := existing.Deleted

	// apply SetDeleted logic
	existing.SetDeleted(mode)

	gc := FromComment(existing)
	if err := p.db.Save(&gc).Error; err != nil {
		return fmt.Errorf("can't save deleted comment for key %s: %w", commentID, err)
	}

	// decrement post info count only if not already deleted
	if !wasDeleted {
		if err := p.upsertPostInfo(existing); err != nil {
			log.Printf("[WARN] failed to update post info after delete for %s: %v", locator.URL, err)
		}
	}

	return nil
}

// deleteAll removes all data for the given site
func (p *PostgresDB) deleteAll(siteID string) error {
	// delete in order to respect any potential constraints
	if err := p.db.Where("site_id = ?", siteID).Delete(&GormComment{}).Error; err != nil {
		return fmt.Errorf("failed to delete all comments for site %s: %w", siteID, err)
	}
	if err := p.db.Where("site_id = ?", siteID).Delete(&GormPostInfo{}).Error; err != nil {
		return fmt.Errorf("failed to delete all post info for site %s: %w", siteID, err)
	}
	if err := p.db.Where("site_id = ?", siteID).Delete(&GormUserDetail{}).Error; err != nil {
		return fmt.Errorf("failed to delete all user details for site %s: %w", siteID, err)
	}
	// note: preserve blocked_users, verified_users, readonly_posts — matching BoltDB behavior
	return nil
}

// deleteUser soft or hard deletes all comments from a user
func (p *PostgresDB) deleteUser(siteID, userID string, mode store.DeleteMode) error {
	// find all non-deleted comments by this user for this site
	var gormComments []GormComment
	if err := p.db.Where("site_id = ? AND user_id = ?", siteID, userID).Find(&gormComments).Error; err != nil {
		return fmt.Errorf("failed to find user comments: %w", err)
	}

	if len(gormComments) == 0 {
		return fmt.Errorf("unknown user %s", userID)
	}

	log.Printf("[DEBUG] comments for removal=%d", len(gormComments))

	// delete each comment
	for _, gc := range gormComments {
		comment := gc.ToComment()
		if err := p.deleteComment(comment.Locator, comment.ID, mode); err != nil {
			return fmt.Errorf("failed to delete comment %s: %w", comment.ID, err)
		}
	}

	// delete user details
	return p.deleteUserDetail(siteID, userID, AllUserDetails)
}

// deleteUserDetail removes user detail entries
func (p *PostgresDB) deleteUserDetail(siteID, userID string, detail UserDetail) error {
	// load existing entry
	var entry GormUserDetail
	result := p.db.Where("site_id = ? AND user_id = ?", siteID, userID).First(&entry)
	if result.Error != nil {
		// no entry to delete
		return nil
	}

	switch detail {
	case UserEmail:
		entry.Email = ""
	case UserTelegram:
		entry.Telegram = ""
	case AllUserDetails:
		entry.Email = ""
		entry.Telegram = ""
	}

	// if no details remain, delete the entry entirely
	if entry.Email == "" && entry.Telegram == "" {
		return p.db.Where("site_id = ? AND user_id = ?", siteID, userID).Delete(&GormUserDetail{}).Error
	}

	// otherwise update
	return p.db.Save(&entry).Error
}

// isReadOnly checks if a post is in the readonly_posts table
func (p *PostgresDB) isReadOnly(locator store.Locator) bool {
	var count int64
	p.db.Model(&GormReadOnlyPost{}).Where("site_id = ? AND url = ?", locator.SiteID, locator.URL).Count(&count)
	return count > 0
}

// placeholder methods to be implemented in later tasks

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
