package main

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func insertUnreadInboxItem(t *testing.T, recipientID string, issueID *string, title string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title)
		VALUES ($1, 'member', $2, 'mentioned', 'info', $3, $4)
	`, testWorkspaceID, recipientID, issueID, title)
	if err != nil {
		t.Fatalf("insertUnreadInboxItem: %v", err)
	}
}

func insertInboxItemWithReadState(t *testing.T, recipientID string, issueID *string, title string, read bool, createdAt time.Time) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, read, created_at)
		VALUES ($1, 'member', $2, 'mentioned', 'info', $3, $4, $5, $6)
	`, testWorkspaceID, recipientID, issueID, title, read, createdAt)
	if err != nil {
		t.Fatalf("insertInboxItemWithReadState: %v", err)
	}
}

func TestCountUnreadInbox_DeduplicatesByIssueAndCountsNullIssueRows(t *testing.T) {
	queries := db.New(testPool)

	recipientEmail := "inbox-count-recipient@multica.ai"
	recipientID := createTestUser(t, recipientEmail)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE workspace_id = $1 AND recipient_id = $2`, testWorkspaceID, recipientID)
		cleanupTestUser(t, recipientEmail)
	})

	issueAID := createTestIssue(t, testWorkspaceID, testUserID)
	issueBID := createTestIssue(t, testWorkspaceID, testUserID)
	issueCID := createTestIssue(t, testWorkspaceID, testUserID)
	t.Cleanup(func() {
		cleanupTestIssue(t, issueAID)
		cleanupTestIssue(t, issueBID)
		cleanupTestIssue(t, issueCID)
	})

	insertUnreadInboxItem(t, recipientID, &issueAID, "issue A first unread")
	insertUnreadInboxItem(t, recipientID, &issueAID, "issue A second unread")
	insertUnreadInboxItem(t, recipientID, &issueBID, "issue B unread")
	insertUnreadInboxItem(t, recipientID, nil, "standalone first unread")
	insertUnreadInboxItem(t, recipientID, nil, "standalone second unread")

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, read)
		VALUES ($1, 'member', $2, 'mentioned', 'info', $3, 'read item ignored', true)
	`, testWorkspaceID, recipientID, issueCID); err != nil {
		t.Fatalf("insert read inbox item: %v", err)
	}

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, archived)
		VALUES ($1, 'member', $2, 'mentioned', 'info', NULL, 'archived item ignored', true)
	`, testWorkspaceID, recipientID); err != nil {
		t.Fatalf("insert archived inbox item: %v", err)
	}

	count, err := queries.CountUnreadInbox(context.Background(), db.CountUnreadInboxParams{
		WorkspaceID:   util.MustParseUUID(testWorkspaceID),
		RecipientType: "member",
		RecipientID:   util.MustParseUUID(recipientID),
	})
	if err != nil {
		t.Fatalf("CountUnreadInbox: %v", err)
	}

	if count != 4 {
		t.Fatalf("expected unread count 4, got %d", count)
	}
}

func TestCountUnreadInbox_UsesLatestIssueRepresentativeReadState(t *testing.T) {
	queries := db.New(testPool)

	recipientEmail := "inbox-count-latest-recipient@multica.ai"
	recipientID := createTestUser(t, recipientEmail)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM inbox_item WHERE workspace_id = $1 AND recipient_id = $2`, testWorkspaceID, recipientID)
		cleanupTestUser(t, recipientEmail)
	})

	issueID := createTestIssue(t, testWorkspaceID, testUserID)
	t.Cleanup(func() {
		cleanupTestIssue(t, issueID)
	})

	now := time.Now().UTC()
	insertInboxItemWithReadState(t, recipientID, &issueID, "older unread issue item", false, now.Add(-time.Minute))
	insertInboxItemWithReadState(t, recipientID, &issueID, "newest read issue item", true, now)
	insertUnreadInboxItem(t, recipientID, nil, "standalone unread")

	count, err := queries.CountUnreadInbox(context.Background(), db.CountUnreadInboxParams{
		WorkspaceID:   util.MustParseUUID(testWorkspaceID),
		RecipientType: "member",
		RecipientID:   util.MustParseUUID(recipientID),
	})
	if err != nil {
		t.Fatalf("CountUnreadInbox: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected unread count 1, got %d", count)
	}
}
