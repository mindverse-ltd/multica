import {
  infiniteQueryOptions,
  useInfiniteQuery,
  type InfiniteData,
} from "@tanstack/react-query";
import { api } from "../api";
import type { InboxItem, InboxPage } from "../types";

export const inboxKeys = {
  all: (wsId: string) => ["inbox", wsId] as const,
  list: (wsId: string) => [...inboxKeys.all(wsId), "list"] as const,
};

export function inboxListOptions(wsId: string) {
  return infiniteQueryOptions({
    queryKey: inboxKeys.list(wsId),
    queryFn: ({ pageParam }) => api.listInbox({ cursor: pageParam, limit: 50 }),
    initialPageParam: null as string | null,
    getNextPageParam: (lastPage) => lastPage.next_cursor,
  });
}

export function getInboxItemsFromPages(
  data: InfiniteData<InboxPage> | undefined,
): InboxItem[] {
  return data?.pages.flatMap((page) => page.items) ?? [];
}

export function mapInboxPages(
  data: InfiniteData<InboxPage> | undefined,
  mapItem: (item: InboxItem) => InboxItem | null,
): InfiniteData<InboxPage> | undefined {
  if (!data) return data;
  return {
    ...data,
    pages: data.pages.map((page) => ({
      ...page,
      items: page.items.map(mapItem).filter((item): item is InboxItem => item !== null),
    })),
  };
}

export function markInboxItemReadInPages(
  data: InfiniteData<InboxPage> | undefined,
  id: string,
): InfiniteData<InboxPage> | undefined {
  return mapInboxPages(data, (item) =>
    item.id === id ? { ...item, read: true } : item,
  );
}

export function archiveInboxItemInPages(
  data: InfiniteData<InboxPage> | undefined,
  id: string,
): InfiniteData<InboxPage> | undefined {
  const target = getInboxItemsFromPages(data).find((item) => item.id === id);
  const issueId = target?.issue_id;
  return mapInboxPages(data, (item) =>
    item.id === id || (issueId && item.issue_id === issueId)
      ? { ...item, archived: true }
      : item,
  );
}

export function markAllInboxReadInPages(
  data: InfiniteData<InboxPage> | undefined,
): InfiniteData<InboxPage> | undefined {
  return mapInboxPages(data, (item) =>
    !item.archived ? { ...item, read: true } : item,
  );
}

/**
 * Unread inbox count for the given workspace, aligned with what the inbox
 * list UI renders: archived items excluded, then deduplicated by issue so a
 * single issue with three unread notifications counts once.
 */
export function useInboxUnreadCount(wsId: string | null | undefined): number {
  const { data } = useInfiniteQuery({
    ...inboxListOptions(wsId ?? ""),
    enabled: !!wsId,
    select: (data) =>
      deduplicateInboxItems(getInboxItemsFromPages(data)).filter((i) => !i.read)
        .length,
  });
  return data ?? 0;
}

/**
 * Deduplicate inbox items by issue_id (one entry per issue, Linear-style).
 * Exported for consumers to use in useMemo — not in queryOptions select
 * (to avoid new array references on every cache update).
 */
export function deduplicateInboxItems(items: InboxItem[]): InboxItem[] {
  const active = items.filter((i) => !i.archived);
  const groups = new Map<string, InboxItem[]>();
  for (const item of active) {
    const key = item.issue_id ?? item.id;
    const group = groups.get(key) ?? [];
    group.push(item);
    groups.set(key, group);
  }
  const merged: InboxItem[] = [];
  for (const group of groups.values()) {
    group.sort(
      (a, b) =>
        new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
    );
    if (group[0]) merged.push(group[0]);
  }
  return merged.sort(
    (a, b) =>
      new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );
}
