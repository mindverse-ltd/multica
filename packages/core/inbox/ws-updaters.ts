import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import { inboxKeys, mapInboxPages } from "./queries";
import type { InboxItem, IssueStatus } from "../types";
import type { InboxPage } from "../types";

export function onInboxNew(
  qc: QueryClient,
  wsId: string,
  item: InboxItem,
) {
  qc.setQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId), (old) => {
    if (!old) {
      return {
        pages: [{ items: [item], next_cursor: null }],
        pageParams: [null],
      };
    }
    const pages = old.pages.map((page) => ({
      ...page,
      items: page.items.filter((i) => i.id !== item.id),
    }));
    const firstPage = pages[0] ?? { items: [], next_cursor: null };
    return {
      ...old,
      pages: [{ ...firstPage, items: [item, ...firstPage.items] }, ...pages.slice(1)],
    };
  });
}

export function onInboxIssueStatusChanged(
  qc: QueryClient,
  wsId: string,
  issueId: string,
  status: IssueStatus,
) {
  qc.setQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId), (old) =>
    mapInboxPages(old, (i) =>
      i.issue_id === issueId ? { ...i, issue_status: status } : i,
    ),
  );
}

// Mirrors the DB-level ON DELETE CASCADE on inbox_item.issue_id: when an issue
// is deleted, all inbox items that referenced it are gone server-side, so drop
// them from the cache too.
export function onInboxIssueDeleted(
  qc: QueryClient,
  wsId: string,
  issueId: string,
) {
  qc.setQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId), (old) =>
    mapInboxPages(old, (i) => (i.issue_id === issueId ? null : i)),
  );
}

export function onInboxInvalidate(qc: QueryClient, wsId: string) {
  qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
}
