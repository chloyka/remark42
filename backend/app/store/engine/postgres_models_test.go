package engine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/remark42/backend/app/store"
)

func TestGormModel_CommentRoundTrip(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	editTS := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)

	original := store.Comment{
		ID:       "comment-1",
		ParentID: "parent-1",
		Text:     "<p>Hello world</p>",
		Orig:     "Hello world",
		User: store.User{
			Name:    "testuser",
			ID:      "user-1",
			Picture: "http://example.com/pic.png",
			IP:      "127.0.0.1",
			Admin:   true,
		},
		Locator: store.Locator{
			SiteID: "site-1",
			URL:    "http://example.com/post/1",
		},
		Score: 5,
		Votes: map[string]bool{
			"user-2": true,
			"user-3": false,
		},
		VotedIPs: map[string]store.VotedIPInfo{
			"hash1": {Timestamp: ts, Value: true},
			"hash2": {Timestamp: ts, Value: false},
		},
		Vote:        1,
		Controversy: 2.5,
		Timestamp:   ts,
		Edit: &store.Edit{
			Timestamp: editTS,
			Summary:   "fixed typo",
		},
		Pin:       true,
		Deleted:   false,
		Imported:  true,
		PostTitle: "Test Post",
	}

	gormComment := FromComment(original)

	// verify GormComment fields
	assert.Equal(t, "comment-1", gormComment.ID)
	assert.Equal(t, "parent-1", gormComment.ParentID)
	assert.Equal(t, "<p>Hello world</p>", gormComment.Text)
	assert.Equal(t, "Hello world", gormComment.Orig)
	assert.Equal(t, "user-1", gormComment.UserID)
	assert.Equal(t, "testuser", gormComment.UserName)
	assert.Equal(t, "http://example.com/pic.png", gormComment.UserPicture)
	assert.Equal(t, "127.0.0.1", gormComment.UserIP)
	assert.True(t, gormComment.UserAdmin)
	assert.Equal(t, "site-1", gormComment.SiteID)
	assert.Equal(t, "http://example.com/post/1", gormComment.URL)
	assert.Equal(t, 5, gormComment.Score)
	assert.Equal(t, 1, gormComment.Vote)
	assert.InDelta(t, 2.5, gormComment.Controversy, 0.001)
	assert.Equal(t, ts, gormComment.Timestamp)
	assert.NotNil(t, gormComment.EditTimestamp)
	assert.Equal(t, "fixed typo", gormComment.EditSummary)
	assert.True(t, gormComment.Pin)
	assert.False(t, gormComment.Deleted)
	assert.True(t, gormComment.Imported)
	assert.Equal(t, "Test Post", gormComment.PostTitle)
	assert.NotEmpty(t, gormComment.Votes, "Votes JSON should not be empty")
	assert.NotEmpty(t, gormComment.VotedIPs, "VotedIPs JSON should not be empty")

	// convert back
	roundTripped := gormComment.ToComment()

	// verify round-trip
	assert.Equal(t, original.ID, roundTripped.ID)
	assert.Equal(t, original.ParentID, roundTripped.ParentID)
	assert.Equal(t, original.Text, roundTripped.Text)
	assert.Equal(t, original.Orig, roundTripped.Orig)
	assert.Equal(t, original.User.Name, roundTripped.User.Name)
	assert.Equal(t, original.User.ID, roundTripped.User.ID)
	assert.Equal(t, original.User.Picture, roundTripped.User.Picture)
	assert.Equal(t, original.User.IP, roundTripped.User.IP)
	assert.Equal(t, original.User.Admin, roundTripped.User.Admin)
	assert.Equal(t, original.Locator.SiteID, roundTripped.Locator.SiteID)
	assert.Equal(t, original.Locator.URL, roundTripped.Locator.URL)
	assert.Equal(t, original.Score, roundTripped.Score)
	assert.Equal(t, original.Vote, roundTripped.Vote)
	assert.InDelta(t, original.Controversy, roundTripped.Controversy, 0.001)
	assert.Equal(t, original.Timestamp, roundTripped.Timestamp)
	assert.Equal(t, original.Pin, roundTripped.Pin)
	assert.Equal(t, original.Deleted, roundTripped.Deleted)
	assert.Equal(t, original.Imported, roundTripped.Imported)
	assert.Equal(t, original.PostTitle, roundTripped.PostTitle)

	// verify votes round-trip
	require.NotNil(t, roundTripped.Votes)
	assert.Equal(t, original.Votes["user-2"], roundTripped.Votes["user-2"])
	assert.Equal(t, original.Votes["user-3"], roundTripped.Votes["user-3"])

	// verify voted IPs round-trip
	require.NotNil(t, roundTripped.VotedIPs)
	assert.Equal(t, original.VotedIPs["hash1"].Value, roundTripped.VotedIPs["hash1"].Value)
	assert.Equal(t, original.VotedIPs["hash2"].Value, roundTripped.VotedIPs["hash2"].Value)

	// verify edit round-trip
	require.NotNil(t, roundTripped.Edit)
	assert.Equal(t, original.Edit.Summary, roundTripped.Edit.Summary)
	assert.Equal(t, original.Edit.Timestamp.UnixNano(), roundTripped.Edit.Timestamp.UnixNano())
}

func TestGormModel_CommentNoEdit(t *testing.T) {
	original := store.Comment{
		ID:   "comment-2",
		Text: "Simple comment",
		User: store.User{
			Name: "user",
			ID:   "user-1",
		},
		Locator: store.Locator{
			SiteID: "site-1",
			URL:    "http://example.com/post/1",
		},
		Timestamp: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC),
	}

	gormComment := FromComment(original)
	assert.Nil(t, gormComment.EditTimestamp)
	assert.Empty(t, gormComment.EditSummary)

	roundTripped := gormComment.ToComment()
	assert.Nil(t, roundTripped.Edit)
}

func TestGormModel_CommentEmptyCollections(t *testing.T) {
	original := store.Comment{
		ID:   "comment-3",
		Text: "No votes",
		User: store.User{
			Name: "user",
			ID:   "user-1",
		},
		Locator: store.Locator{
			SiteID: "site-1",
			URL:    "http://example.com/post/1",
		},
		Timestamp: time.Now(),
	}

	gormComment := FromComment(original)
	assert.Empty(t, gormComment.Votes)
	assert.Empty(t, gormComment.VotedIPs)

	roundTripped := gormComment.ToComment()
	assert.Nil(t, roundTripped.Votes)
	assert.Nil(t, roundTripped.VotedIPs)
}

func TestGormModel_PostInfoToPostInfo(t *testing.T) {
	ts1 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ts2 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	gpi := GormPostInfo{
		URL:    "http://example.com/post/1",
		SiteID: "site-1",
		Count:  42,
		FirstTS: ts1,
		LastTS:  ts2,
	}

	result := gpi.ToPostInfo()
	assert.Equal(t, "http://example.com/post/1", result.URL)
	assert.Equal(t, 42, result.Count)
	assert.Equal(t, ts1, result.FirstTS)
	assert.Equal(t, ts2, result.LastTS)
}

func TestGormModel_BlockedUserToBlockedUser(t *testing.T) {
	until := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	gbu := GormBlockedUser{
		SiteID: "site-1",
		UserID: "user-bad",
		Name:   "Bad User",
		Until:  until,
	}

	result := gbu.ToBlockedUser()
	assert.Equal(t, "user-bad", result.ID)
	assert.Equal(t, "Bad User", result.Name)
	assert.Equal(t, until, result.Until)
}

func TestGormModel_UserDetailToUserDetailEntry(t *testing.T) {
	gud := GormUserDetail{
		SiteID:   "site-1",
		UserID:   "user-1",
		Email:    "user@example.com",
		Telegram: "@testuser",
	}

	result := gud.ToUserDetailEntry()
	assert.Equal(t, "user-1", result.UserID)
	assert.Equal(t, "user@example.com", result.Email)
	assert.Equal(t, "@testuser", result.Telegram)
}
