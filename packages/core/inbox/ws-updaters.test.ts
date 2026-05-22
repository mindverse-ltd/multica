import { describe, it, expect, vi } from "vitest";
import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import {
  onInboxIssueDeleted,
  onInboxIssueStatusChanged,
  onInboxNew,
} from "./ws-updaters";
import {
  archiveInboxItemInPages,
  getInboxItemsFromPages,
  inboxKeys,
  markAllInboxReadInPages,
} from "./queries";
import type { InboxItem, InboxPage } from "../types";

const wsId = "ws-1";

function makeItem(
  id: string,
  issueId: string | null,
  overrides: Partial<InboxItem> = {},
): InboxItem {
  return {
    id,
    workspace_id: wsId,
    recipient_type: "member",
    recipient_id: "user-1",
    actor_type: null,
    actor_id: null,
    type: "mentioned",
    severity: "info",
    issue_id: issueId,
    title: `item ${id}`,
    body: null,
    issue_status: null,
    read: false,
    archived: false,
    created_at: "2025-01-01T00:00:00Z",
    details: null,
    ...overrides,
  };
}

function makePages(pages: InboxItem[][]): InfiniteData<InboxPage> {
  return {
    pages: pages.map((items, index) => ({
      items,
      next_cursor: index < pages.length - 1 ? `cursor-${index + 1}` : null,
    })),
    pageParams: [null, ...pages.slice(1).map((_, index) => `cursor-${index + 1}`)],
  };
}

function setInboxPages(qc: QueryClient, pages: InboxItem[][]) {
  qc.setQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId), makePages(pages));
}

function getInboxPages(qc: QueryClient) {
  return qc.getQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId));
}

function getInboxItems(qc: QueryClient) {
  return getInboxItemsFromPages(getInboxPages(qc));
}

describe("onInboxIssueDeleted", () => {
  it("removes all inbox items referencing the deleted issue", () => {
    const qc = new QueryClient();
    setInboxPages(qc, [
      [makeItem("i1", "issue-a"), makeItem("i3", "issue-b")],
      [makeItem("i2", "issue-a"), makeItem("i4", null)],
    ]);

    onInboxIssueDeleted(qc, wsId, "issue-a");

    expect(getInboxItems(qc).map((i) => i.id)).toEqual(["i3", "i4"]);
  });

  it("is a no-op when the inbox cache is empty", () => {
    const qc = new QueryClient();
    expect(() => onInboxIssueDeleted(qc, wsId, "issue-a")).not.toThrow();
    expect(getInboxPages(qc)).toBeUndefined();
  });
});

describe("onInboxIssueStatusChanged", () => {
  it("updates issue_status only for items referencing the issue", () => {
    const qc = new QueryClient();
    setInboxPages(qc, [
      [makeItem("i1", "issue-a", { issue_status: "todo" })],
      [makeItem("i2", "issue-b", { issue_status: "todo" })],
    ]);

    onInboxIssueStatusChanged(qc, wsId, "issue-a", "done");

    const after = getInboxItems(qc);
    expect(after.find((i) => i.id === "i1")?.issue_status).toBe("done");
    expect(after.find((i) => i.id === "i2")?.issue_status).toBe("todo");
  });
});

describe("onInboxNew", () => {
  it("inserts the new item at the top of page 0 without invalidating the list", () => {
    const qc = new QueryClient();
    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
    setInboxPages(qc, [
      [makeItem("i1", "issue-a")],
      [makeItem("i2", "issue-b")],
    ]);

    onInboxNew(qc, wsId, makeItem("i-new", "issue-new"));

    const pages = getInboxPages(qc);
    expect(pages?.pages[0]?.items.map((i) => i.id)).toEqual(["i-new", "i1"]);
    expect(pages?.pages[1]?.items.map((i) => i.id)).toEqual(["i2"]);
    expect(invalidateSpy).not.toHaveBeenCalled();
  });

  it("deduplicates the pushed item across pages", () => {
    const qc = new QueryClient();
    setInboxPages(qc, [
      [makeItem("i1", "issue-a")],
      [makeItem("i-new", "issue-new")],
    ]);

    onInboxNew(qc, wsId, makeItem("i-new", "issue-new", { read: true }));

    expect(getInboxItems(qc).map((i) => i.id)).toEqual(["i-new", "i1"]);
    expect(getInboxItems(qc).find((i) => i.id === "i-new")?.read).toBe(true);
  });
});

describe("inbox optimistic page helpers", () => {
  it("archives every item for the same issue across page boundaries", () => {
    const pages = makePages([
      [makeItem("i1", "issue-a"), makeItem("i2", "issue-b")],
      [makeItem("i3", "issue-a"), makeItem("i4", null)],
    ]);

    const after = archiveInboxItemInPages(pages, "i1");
    const items = getInboxItemsFromPages(after);

    expect(items.find((i) => i.id === "i1")?.archived).toBe(true);
    expect(items.find((i) => i.id === "i3")?.archived).toBe(true);
    expect(items.find((i) => i.id === "i2")?.archived).toBe(false);
    expect(items.find((i) => i.id === "i4")?.archived).toBe(false);
  });

  it("marks every non-archived item read across page boundaries", () => {
    const pages = makePages([
      [makeItem("i1", "issue-a"), makeItem("i2", "issue-b", { archived: true })],
      [makeItem("i3", "issue-c")],
    ]);

    const after = markAllInboxReadInPages(pages);
    const items = getInboxItemsFromPages(after);

    expect(items.find((i) => i.id === "i1")?.read).toBe(true);
    expect(items.find((i) => i.id === "i2")?.read).toBe(false);
    expect(items.find((i) => i.id === "i3")?.read).toBe(true);
  });
});
