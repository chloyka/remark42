package engine

import (
	"errors"
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

	closeDB := func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
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
		closeDB()
		return nil, fmt.Errorf("failed to auto-migrate postgres tables: %w", err)
	}

	// verify connectivity
	var result int
	if err = db.Raw("SELECT 1").Scan(&result).Error; err != nil {
		closeDB()
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
	ro, err := p.isReadOnly(comment.Locator)
	if err != nil {
		return "", fmt.Errorf("failed to check read-only for %s: %w", comment.Locator.URL, err)
	}
	if ro {
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

	// get first and last timestamps for non-deleted comments
	var firstTS, lastTS time.Time
	if cnt > 0 {
		row := p.db.Model(&GormComment{}).
			Where("site_id = ? AND url = ? AND deleted = ?", comment.Locator.SiteID, comment.Locator.URL, false).
			Select("MIN(timestamp), MAX(timestamp)").Row()
		if err := row.Scan(&firstTS, &lastTS); err != nil {
			return fmt.Errorf("failed to get timestamps for post info: %w", err)
		}
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
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil // no entry to delete
		}
		return fmt.Errorf("failed to load user detail for deletion: %w", result.Error)
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
func (p *PostgresDB) isReadOnly(locator store.Locator) (bool, error) {
	var count int64
	if err := p.db.Model(&GormReadOnlyPost{}).Where("site_id = ? AND url = ?", locator.SiteID, locator.URL).Count(&count).Error; err != nil {
		return false, fmt.Errorf("failed to check read-only status: %w", err)
	}
	return count > 0, nil
}

// Find returns comments matching the request.
// Supports finding by post (URL+SiteID), by user (UserID+SiteID), or last N comments for site.
func (p *PostgresDB) Find(req FindRequest) ([]store.Comment, error) {
	comments := []store.Comment{}

	var err error
	switch {
	case req.Locator.SiteID != "" && req.Locator.URL != "": // find post comments
		comments, err = p.findForPost(req)
		if err != nil {
			return nil, err
		}
	case req.Locator.SiteID != "" && req.UserID != "": // find comments for user
		comments, err = p.findForUser(req)
		if err != nil {
			return nil, err
		}
	case req.Locator.SiteID != "" && req.Locator.URL == "" && req.UserID == "": // find last comments for site
		comments, err = p.findLastForSite(req)
		if err != nil {
			return nil, err
		}
	}

	return SortComments(comments, req.Sort), nil
}

// findForPost retrieves all comments for a specific post, optionally filtering by Since.
func (p *PostgresDB) findForPost(req FindRequest) ([]store.Comment, error) {
	var gormComments []GormComment
	query := p.db.Where("site_id = ? AND url = ?", req.Locator.SiteID, req.Locator.URL)
	if !req.Since.IsZero() {
		query = query.Where("timestamp > ?", req.Since)
	}
	if err := query.Find(&gormComments).Error; err != nil {
		return nil, fmt.Errorf("failed to find comments for post %s: %w", req.Locator.URL, err)
	}

	comments := make([]store.Comment, 0, len(gormComments))
	for _, gc := range gormComments {
		comments = append(comments, gc.ToComment())
	}
	return comments, nil
}

// findForUser retrieves comments by user for a site, with limit and skip.
func (p *PostgresDB) findForUser(req FindRequest) ([]store.Comment, error) {
	limit := req.Limit
	if limit == 0 || limit > userLimit {
		limit = userLimit
	}

	var gormComments []GormComment
	query := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).
		Order("timestamp DESC")
	if req.Skip > 0 {
		query = query.Offset(req.Skip)
	}
	query = query.Limit(limit)
	if err := query.Find(&gormComments).Error; err != nil {
		return nil, fmt.Errorf("failed to find comments for user %s: %w", req.UserID, err)
	}

	comments := make([]store.Comment, 0, len(gormComments))
	for _, gc := range gormComments {
		comments = append(comments, gc.ToComment())
	}
	return comments, nil
}

// findLastForSite retrieves last N comments for a site, excluding deleted, optionally filtering by Since.
func (p *PostgresDB) findLastForSite(req FindRequest) ([]store.Comment, error) {
	limit := req.Limit
	if limit == 0 || limit > lastLimit {
		limit = lastLimit
	}

	var gormComments []GormComment
	query := p.db.Where("site_id = ? AND deleted = ?", req.Locator.SiteID, false).
		Order("timestamp DESC")
	if !req.Since.IsZero() {
		query = query.Where("timestamp > ?", req.Since)
	}
	query = query.Limit(limit)
	if err := query.Find(&gormComments).Error; err != nil {
		return nil, fmt.Errorf("failed to find last comments for site %s: %w", req.Locator.SiteID, err)
	}

	comments := make([]store.Comment, 0, len(gormComments))
	for _, gc := range gormComments {
		comments = append(comments, gc.ToComment())
	}
	return comments, nil
}

// Count returns the number of comments matching the find criteria.
func (p *PostgresDB) Count(req FindRequest) (int, error) {
	var count int64

	switch {
	case req.Locator.URL != "": // count for post
		if err := p.db.Model(&GormComment{}).
			Where("site_id = ? AND url = ? AND deleted = ?", req.Locator.SiteID, req.Locator.URL, false).
			Count(&count).Error; err != nil {
			return 0, fmt.Errorf("failed to count comments for post: %w", err)
		}
		return int(count), nil
	case req.UserID != "": // count for user
		if err := p.db.Model(&GormComment{}).
			Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).
			Count(&count).Error; err != nil {
			return 0, fmt.Errorf("failed to count comments for user: %w", err)
		}
		if count == 0 {
			return 0, fmt.Errorf("no comments for user %s in store for %s site", req.UserID, req.Locator.SiteID)
		}
		return int(count), nil
	}

	return 0, fmt.Errorf("invalid count request %+v", req)
}

// Info returns post metadata. If URL is set, returns info for a single post.
// If only SiteID is set, returns info for all posts with Limit/Skip pagination.
func (p *PostgresDB) Info(req InfoRequest) ([]store.PostInfo, error) {
	if req.Locator.URL != "" { // single post info
		return p.infoForPost(req)
	}
	if req.Locator.SiteID != "" { // all posts for site
		return p.infoForSite(req)
	}
	return nil, fmt.Errorf("invalid info request %+v", req)
}

// infoForPost returns info for a single post.
func (p *PostgresDB) infoForPost(req InfoRequest) ([]store.PostInfo, error) {
	var gpi GormPostInfo
	result := p.db.Where("site_id = ? AND url = ?", req.Locator.SiteID, req.Locator.URL).First(&gpi)
	if result.Error != nil {
		return nil, fmt.Errorf("can't load info for %s: %w", req.Locator.URL, result.Error)
	}

	info := gpi.ToPostInfo()

	// set read-only from age
	if req.ReadOnlyAge > 0 && !info.FirstTS.IsZero() &&
		info.FirstTS.AddDate(0, 0, req.ReadOnlyAge).Before(time.Now()) {
		info.ReadOnly = true
	}

	// also check manual read-only flag
	if !info.ReadOnly {
		ro, roErr := p.isReadOnly(req.Locator)
		if roErr != nil {
			return nil, fmt.Errorf("failed to check read-only for %s: %w", req.Locator.URL, roErr)
		}
		if ro {
			info.ReadOnly = true
		}
	}

	return []store.PostInfo{info}, nil
}

// infoForSite returns info for all posts of a site with pagination.
func (p *PostgresDB) infoForSite(req InfoRequest) ([]store.PostInfo, error) {
	var gormInfos []GormPostInfo
	query := p.db.Where("site_id = ?", req.Locator.SiteID).Order("last_ts DESC")
	if req.Skip > 0 {
		query = query.Offset(req.Skip)
	}
	if req.Limit > 0 {
		query = query.Limit(req.Limit)
	}
	if err := query.Find(&gormInfos).Error; err != nil {
		return nil, fmt.Errorf("failed to list post info for site %s: %w", req.Locator.SiteID, err)
	}

	result := make([]store.PostInfo, 0, len(gormInfos))
	for i := range gormInfos {
		result = append(result, gormInfos[i].ToPostInfo())
	}
	return result, nil
}

// Flag gets or sets flag values.
// When Update is FlagNonSet, it returns the current value (get mode).
// When Update is FlagTrue or FlagFalse, it sets the value (set mode).
func (p *PostgresDB) Flag(req FlagRequest) (bool, error) {
	if req.Update == FlagNonSet {
		return p.checkFlag(req)
	}
	return p.setFlag(req)
}

// checkFlag returns the current value of a flag
func (p *PostgresDB) checkFlag(req FlagRequest) (bool, error) {
	switch req.Flag {
	case ReadOnly:
		return p.isReadOnly(req.Locator)
	case Verified:
		var count int64
		if err := p.db.Model(&GormVerifiedUser{}).Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).Count(&count).Error; err != nil {
			return false, fmt.Errorf("failed to check verified flag: %w", err)
		}
		return count > 0, nil
	case Blocked:
		var bu GormBlockedUser
		result := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).First(&bu)
		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return false, nil
			}
			return false, fmt.Errorf("failed to check blocked flag: %w", result.Error)
		}
		return time.Now().Before(bu.Until), nil
	}
	return false, nil
}

// setFlag sets a flag value and returns the resulting state
func (p *PostgresDB) setFlag(req FlagRequest) (bool, error) {
	switch req.Flag {
	case ReadOnly:
		return p.setReadOnlyFlag(req)
	case Verified:
		return p.setVerifiedFlag(req)
	case Blocked:
		return p.setBlockedFlag(req)
	}
	return false, fmt.Errorf("unsupported flag %v", req.Flag)
}

// setReadOnlyFlag sets or clears the read-only flag for a post
func (p *PostgresDB) setReadOnlyFlag(req FlagRequest) (bool, error) {
	switch req.Update {
	case FlagTrue:
		entry := GormReadOnlyPost{SiteID: req.Locator.SiteID, URL: req.Locator.URL}
		if err := p.db.Where("site_id = ? AND url = ?", req.Locator.SiteID, req.Locator.URL).
			FirstOrCreate(&entry).Error; err != nil {
			return false, fmt.Errorf("failed to set read-only flag: %w", err)
		}
		return true, nil
	case FlagFalse:
		if err := p.db.Where("site_id = ? AND url = ?", req.Locator.SiteID, req.Locator.URL).
			Delete(&GormReadOnlyPost{}).Error; err != nil {
			return false, fmt.Errorf("failed to clear read-only flag: %w", err)
		}
		return false, nil
	}
	return false, nil
}

// setVerifiedFlag sets or clears the verified flag for a user
func (p *PostgresDB) setVerifiedFlag(req FlagRequest) (bool, error) {
	switch req.Update {
	case FlagTrue:
		entry := GormVerifiedUser{SiteID: req.Locator.SiteID, UserID: req.UserID}
		if err := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).
			FirstOrCreate(&entry).Error; err != nil {
			return false, fmt.Errorf("failed to set verified flag: %w", err)
		}
		return true, nil
	case FlagFalse:
		if err := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).
			Delete(&GormVerifiedUser{}).Error; err != nil {
			return false, fmt.Errorf("failed to clear verified flag: %w", err)
		}
		return false, nil
	}
	return false, nil
}

// setBlockedFlag sets or clears the blocked flag for a user
func (p *PostgresDB) setBlockedFlag(req FlagRequest) (bool, error) {
	switch req.Update {
	case FlagTrue:
		until := time.Now().AddDate(100, 0, 0) // permanent block = 100 years
		if req.TTL > 0 {
			until = time.Now().Add(req.TTL)
		}
		// look up user name from a recent comment
		userName := ""
		findReq := FindRequest{Locator: store.Locator{SiteID: req.Locator.SiteID}, UserID: req.UserID, Limit: 1}
		userComments, err := p.Find(findReq)
		if err == nil && len(userComments) > 0 {
			userName = userComments[0].User.Name
		}

		entry := GormBlockedUser{SiteID: req.Locator.SiteID, UserID: req.UserID, Name: userName, Until: until}
		// upsert: create or update
		if err := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).
			Assign(GormBlockedUser{Until: until, Name: userName}).
			FirstOrCreate(&entry).Error; err != nil {
			return false, fmt.Errorf("failed to set blocked flag: %w", err)
		}
		return true, nil
	case FlagFalse:
		if err := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).
			Delete(&GormBlockedUser{}).Error; err != nil {
			return false, fmt.Errorf("failed to clear blocked flag: %w", err)
		}
		return false, nil
	}
	return false, nil
}

// ListFlags returns a list of flagged entries for a site.
// For Verified: returns list of verified user IDs (string).
// For Blocked: returns list of store.BlockedUser (filtering out expired blocks).
func (p *PostgresDB) ListFlags(req FlagRequest) ([]interface{}, error) {
	res := []interface{}{}
	switch req.Flag {
	case Verified:
		var entries []GormVerifiedUser
		if err := p.db.Where("site_id = ?", req.Locator.SiteID).Find(&entries).Error; err != nil {
			return nil, fmt.Errorf("failed to list verified users: %w", err)
		}
		for _, e := range entries {
			res = append(res, e.UserID)
		}
		return res, nil
	case Blocked:
		var entries []GormBlockedUser
		if err := p.db.Where("site_id = ?", req.Locator.SiteID).Find(&entries).Error; err != nil {
			return nil, fmt.Errorf("failed to list blocked users: %w", err)
		}
		for _, e := range entries {
			if time.Now().Before(e.Until) {
				res = append(res, e.ToBlockedUser())
			}
		}
		return res, nil
	}
	return nil, fmt.Errorf("flag %s not listable", req.Flag)
}

// UserDetail gets or sets user detail values.
// For UserEmail/UserTelegram with Update set: upserts the value, returns the full entry.
// For UserEmail/UserTelegram without Update: returns the requested detail only.
// For AllUserDetails without UserID and Update: lists all user details for the site.
func (p *PostgresDB) UserDetail(req UserDetailRequest) ([]UserDetailEntry, error) {
	switch req.Detail {
	case UserEmail, UserTelegram:
		if req.UserID == "" {
			return nil, fmt.Errorf("userid cannot be empty in request for single detail")
		}
		if req.Update == "" {
			return p.getUserDetail(req)
		}
		return p.setUserDetail(req)
	case AllUserDetails:
		if req.Update == "" && req.UserID == "" {
			return p.listDetails(req.Locator)
		}
		return nil, fmt.Errorf("unsupported request with userdetail all")
	default:
		return nil, fmt.Errorf("unsupported detail %q", req.Detail)
	}
}

// getUserDetail returns the requested single detail for a user
func (p *PostgresDB) getUserDetail(req UserDetailRequest) ([]UserDetailEntry, error) {
	var entry GormUserDetail
	result := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).First(&entry)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, nil // no entry found — return empty result (matching BoltDB behavior)
		}
		return nil, fmt.Errorf("failed to load user detail: %w", result.Error)
	}

	switch req.Detail {
	case UserEmail:
		return []UserDetailEntry{{UserID: req.UserID, Email: entry.Email}}, nil
	case UserTelegram:
		return []UserDetailEntry{{UserID: req.UserID, Telegram: entry.Telegram}}, nil
	}
	return nil, nil
}

// setUserDetail upserts a single user detail value
func (p *PostgresDB) setUserDetail(req UserDetailRequest) ([]UserDetailEntry, error) {
	var entry GormUserDetail
	result := p.db.Where("site_id = ? AND user_id = ?", req.Locator.SiteID, req.UserID).First(&entry)
	isNew := false
	if result.Error != nil {
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to lookup user detail: %w", result.Error)
		}
		// create new entry
		isNew = true
		entry = GormUserDetail{
			SiteID: req.Locator.SiteID,
			UserID: req.UserID,
		}
	}

	switch req.Detail {
	case UserEmail:
		entry.Email = req.Update
	case UserTelegram:
		entry.Telegram = req.Update
	}

	if isNew {
		// new entry — create
		if err := p.db.Create(&entry).Error; err != nil {
			return nil, fmt.Errorf("failed to create user detail: %w", err)
		}
	} else {
		// existing entry — save
		if err := p.db.Save(&entry).Error; err != nil {
			return nil, fmt.Errorf("failed to update user detail: %w", err)
		}
	}

	return []UserDetailEntry{entry.ToUserDetailEntry()}, nil
}

// listDetails lists all user details for a site
func (p *PostgresDB) listDetails(loc store.Locator) ([]UserDetailEntry, error) {
	var entries []GormUserDetail
	if err := p.db.Where("site_id = ?", loc.SiteID).Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("failed to list user details: %w", err)
	}

	result := make([]UserDetailEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, e.ToUserDetailEntry())
	}
	return result, nil
}
