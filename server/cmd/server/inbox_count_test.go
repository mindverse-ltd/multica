package main

import (
	"context"
	"testing"

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
	t.Cleanup(func() {
		cleanupTestIssue(t, issueAID)
		cleanupTestIssue(t, issueBID)
	})

	insertUnreadInboxItem(t, recipientID, &issueAID, "issue A first unread")
	insertUnreadInboxItem(t, recipientID, &issueAID, "issue A second unread")
	insertUnreadInboxItem(t, recipientID, &issueBID, "issue B unread")
	insertUnreadInboxItem(t, recipientID, nil, "standalone first unread")
	insertUnreadInboxItem(t, recipientID, nil, "standalone second unread")

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO inbox_item (workspace_id, recipient_type, recipient_id, type, severity, issue_id, title, read)
		VALUES ($1, 'member', $2, 'mentioned', 'info', $3, 'read item ignored', true)
	`, testWorkspaceID, recipientID, issueBID); err != nil {
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
