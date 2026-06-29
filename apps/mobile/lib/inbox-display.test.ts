import { describe, expect, it } from "vitest";
import type { InboxItem } from "@multica/core/types";
import { deduplicateInboxItems } from "./inbox-display";

function item(overrides: Partial<InboxItem>): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "workspace-1",
    recipient_type: "member",
    recipient_id: "member-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "new_comment",
    severity: "info",
    issue_id: "issue-1",
    title: "Issue title",
    body: null,
    issue_status: null,
    read: false,
    archived: false,
    created_at: "2026-06-15T08:00:00Z",
    details: null,
    ...overrides,
  };
}

describe("deduplicateInboxItems", () => {
  it("keeps the newest issue row while preserving an older comment anchor", () => {
    const merged = deduplicateInboxItems([
      item({
        id: "comment-notification",
        created_at: "2026-06-15T08:00:00Z",
        details: { comment_id: "comment-1" },
      }),
      item({
        id: "status-notification",
        type: "status_changed",
        created_at: "2026-06-15T08:01:00Z",
        details: { from: "in_progress", to: "in_review" },
      }),
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      id: "status-notification",
      type: "status_changed",
      details: {
        from: "in_progress",
        to: "in_review",
        comment_id: "comment-1",
      },
    });
  });

  it("pins unread representatives above read ones, newest-first within each group", () => {
    const merged = deduplicateInboxItems([
      item({ id: "unread-old", issue_id: "issue-unread", read: false, created_at: "2026-06-15T07:00:00Z" }),
      item({ id: "read-new", issue_id: "issue-read", read: true, created_at: "2026-06-15T09:00:00Z" }),
      item({ id: "unread-new", issue_id: "issue-unread-2", read: false, created_at: "2026-06-15T08:30:00Z" }),
      item({ id: "read-old", issue_id: "issue-read-2", read: true, created_at: "2026-06-15T06:00:00Z" }),
    ]);

    expect(merged.map((i) => i.id)).toEqual([
      "unread-new",
      "unread-old",
      "read-new",
      "read-old",
    ]);
  });
});
