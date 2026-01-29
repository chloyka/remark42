package engine

import (
	"encoding/json"
	"time"

	"github.com/umputun/remark42/backend/app/store"
)

// GormComment maps to the "comments" table
type GormComment struct {
	ID            string    `gorm:"column:id;primaryKey"`
	ParentID      string    `gorm:"column:parent_id"`
	Text          string    `gorm:"column:text;type:text"`
	Orig          string    `gorm:"column:orig;type:text"`
	UserID        string    `gorm:"column:user_id;index:idx_comments_site_user,priority:2"`
	UserName      string    `gorm:"column:user_name"`
	UserPicture   string    `gorm:"column:user_picture"`
	UserIP        string    `gorm:"column:user_ip"`
	UserAdmin     bool      `gorm:"column:user_admin"`
	SiteID        string    `gorm:"column:site_id;index:idx_comments_site_url,priority:1;index:idx_comments_site_user,priority:1;index:idx_comments_site_ts,priority:1"`
	URL           string    `gorm:"column:url;index:idx_comments_site_url,priority:2"`
	Score         int       `gorm:"column:score"`
	Votes         string    `gorm:"column:votes;type:text"`
	VotedIPs      string    `gorm:"column:voted_ips;type:text"`
	Vote          int       `gorm:"column:vote"`
	Controversy   float64   `gorm:"column:controversy"`
	Timestamp     time.Time `gorm:"column:timestamp;index:idx_comments_site_ts,priority:2"`
	EditTimestamp *int64    `gorm:"column:edit_timestamp"`
	EditSummary   string    `gorm:"column:edit_summary"`
	Pin           bool      `gorm:"column:pin"`
	Deleted       bool      `gorm:"column:deleted"`
	Imported      bool      `gorm:"column:imported"`
	PostTitle     string    `gorm:"column:post_title"`
}

// TableName overrides the table name
func (GormComment) TableName() string { return "comments" }

// GormPostInfo maps to the "post_info" table
type GormPostInfo struct {
	URL      string    `gorm:"column:url;primaryKey"`
	SiteID   string    `gorm:"column:site_id;primaryKey"`
	Count    int       `gorm:"column:count"`
	ReadOnly bool      `gorm:"column:read_only"`
	FirstTS  time.Time `gorm:"column:first_ts"`
	LastTS   time.Time `gorm:"column:last_ts"`
}

// TableName overrides the table name
func (GormPostInfo) TableName() string { return "post_info" }

// GormBlockedUser maps to the "blocked_users" table
type GormBlockedUser struct {
	SiteID string    `gorm:"column:site_id;primaryKey"`
	UserID string    `gorm:"column:user_id;primaryKey"`
	Name   string    `gorm:"column:name"`
	Until  time.Time `gorm:"column:until"`
}

// TableName overrides the table name
func (GormBlockedUser) TableName() string { return "blocked_users" }

// GormVerifiedUser maps to the "verified_users" table
type GormVerifiedUser struct {
	SiteID string `gorm:"column:site_id;primaryKey"`
	UserID string `gorm:"column:user_id;primaryKey"`
}

// TableName overrides the table name
func (GormVerifiedUser) TableName() string { return "verified_users" }

// GormReadOnlyPost maps to the "readonly_posts" table
type GormReadOnlyPost struct {
	SiteID string `gorm:"column:site_id;primaryKey"`
	URL    string `gorm:"column:url;primaryKey"`
}

// TableName overrides the table name
func (GormReadOnlyPost) TableName() string { return "readonly_posts" }

// GormUserDetail maps to the "user_details" table
type GormUserDetail struct {
	SiteID   string `gorm:"column:site_id;primaryKey"`
	UserID   string `gorm:"column:user_id;primaryKey"`
	Email    string `gorm:"column:email"`
	Telegram string `gorm:"column:telegram"`
}

// TableName overrides the table name
func (GormUserDetail) TableName() string { return "user_details" }

// ToComment converts GormComment to store.Comment
func (g *GormComment) ToComment() store.Comment {
	c := store.Comment{
		ID:       g.ID,
		ParentID: g.ParentID,
		Text:     g.Text,
		Orig:     g.Orig,
		User: store.User{
			Name:    g.UserName,
			ID:      g.UserID,
			Picture: g.UserPicture,
			IP:      g.UserIP,
			Admin:   g.UserAdmin,
		},
		Locator: store.Locator{
			SiteID: g.SiteID,
			URL:    g.URL,
		},
		Score:       g.Score,
		Vote:        g.Vote,
		Controversy: g.Controversy,
		Timestamp:   g.Timestamp,
		Pin:         g.Pin,
		Deleted:     g.Deleted,
		Imported:    g.Imported,
		PostTitle:   g.PostTitle,
	}

	if g.Votes != "" {
		_ = json.Unmarshal([]byte(g.Votes), &c.Votes)
	}
	if g.VotedIPs != "" {
		_ = json.Unmarshal([]byte(g.VotedIPs), &c.VotedIPs)
	}
	if g.EditTimestamp != nil {
		ts := time.Unix(0, *g.EditTimestamp)
		c.Edit = &store.Edit{
			Timestamp: ts,
			Summary:   g.EditSummary,
		}
	}

	return c
}

// FromComment converts store.Comment to GormComment
func FromComment(c store.Comment) GormComment {
	g := GormComment{
		ID:          c.ID,
		ParentID:    c.ParentID,
		Text:        c.Text,
		Orig:        c.Orig,
		UserID:      c.User.ID,
		UserName:    c.User.Name,
		UserPicture: c.User.Picture,
		UserIP:      c.User.IP,
		UserAdmin:   c.User.Admin,
		SiteID:      c.Locator.SiteID,
		URL:         c.Locator.URL,
		Score:       c.Score,
		Vote:        c.Vote,
		Controversy: c.Controversy,
		Timestamp:   c.Timestamp,
		Pin:         c.Pin,
		Deleted:     c.Deleted,
		Imported:    c.Imported,
		PostTitle:   c.PostTitle,
	}

	if c.Votes != nil {
		data, _ := json.Marshal(c.Votes)
		g.Votes = string(data)
	}
	if c.VotedIPs != nil {
		data, _ := json.Marshal(c.VotedIPs)
		g.VotedIPs = string(data)
	}
	if c.Edit != nil {
		ts := c.Edit.Timestamp.UnixNano()
		g.EditTimestamp = &ts
		g.EditSummary = c.Edit.Summary
	}

	return g
}

// ToPostInfo converts GormPostInfo to store.PostInfo
func (g *GormPostInfo) ToPostInfo() store.PostInfo {
	return store.PostInfo{
		URL:      g.URL,
		Count:    g.Count,
		ReadOnly: g.ReadOnly,
		FirstTS:  g.FirstTS,
		LastTS:   g.LastTS,
	}
}

// FromPostInfo converts store.PostInfo to GormPostInfo for a given siteID
func FromPostInfo(p store.PostInfo, siteID string) GormPostInfo {
	return GormPostInfo{
		URL:      p.URL,
		SiteID:   siteID,
		Count:    p.Count,
		ReadOnly: p.ReadOnly,
		FirstTS:  p.FirstTS,
		LastTS:   p.LastTS,
	}
}

// ToBlockedUser converts GormBlockedUser to store.BlockedUser
func (g *GormBlockedUser) ToBlockedUser() store.BlockedUser {
	return store.BlockedUser{
		ID:    g.UserID,
		Name:  g.Name,
		Until: g.Until,
	}
}

// FromBlockedUser converts store.BlockedUser to GormBlockedUser for a given siteID
func FromBlockedUser(b store.BlockedUser, siteID string) GormBlockedUser {
	return GormBlockedUser{
		SiteID: siteID,
		UserID: b.ID,
		Name:   b.Name,
		Until:  b.Until,
	}
}

// ToUserDetailEntry converts GormUserDetail to UserDetailEntry
func (g *GormUserDetail) ToUserDetailEntry() UserDetailEntry {
	return UserDetailEntry{
		UserID:   g.UserID,
		Email:    g.Email,
		Telegram: g.Telegram,
	}
}

// FromUserDetailEntry converts UserDetailEntry to GormUserDetail for a given siteID
func FromUserDetailEntry(e UserDetailEntry, siteID string) GormUserDetail {
	return GormUserDetail{
		SiteID:   siteID,
		UserID:   e.UserID,
		Email:    e.Email,
		Telegram: e.Telegram,
	}
}
