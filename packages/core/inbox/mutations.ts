import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import {
  archiveInboxItemInPages,
  inboxKeys,
  markAllInboxReadInPages,
  markInboxItemReadInPages,
} from "./queries";
import { useWorkspaceId } from "../hooks";
import type { InfiniteData } from "@tanstack/react-query";
import type { InboxPage } from "../types";

export function useMarkInboxRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.markInboxRead(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId));
      qc.setQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId), (old) =>
        markInboxItemReadInPages(old, id),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
    },
  });
}

export function useArchiveInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: (id: string) => api.archiveInbox(id),
    onMutate: async (id) => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId));
      qc.setQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId), (old) =>
        archiveInboxItemInPages(old, id),
      );
      return { prev };
    },
    onError: (_err, _id, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
    },
  });
}

export function useMarkAllInboxRead() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.markAllInboxRead(),
    onMutate: async () => {
      await qc.cancelQueries({ queryKey: inboxKeys.list(wsId) });
      const prev = qc.getQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId));
      qc.setQueryData<InfiniteData<InboxPage>>(inboxKeys.list(wsId), (old) =>
        markAllInboxReadInPages(old),
      );
      return { prev };
    },
    onError: (_err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(inboxKeys.list(wsId), ctx.prev);
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
    },
  });
}

export function useArchiveAllInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveAllInbox(),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
    },
  });
}

export function useArchiveAllReadInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveAllReadInbox(),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
    },
  });
}

export function useArchiveCompletedInbox() {
  const qc = useQueryClient();
  const wsId = useWorkspaceId();
  return useMutation({
    mutationFn: () => api.archiveCompletedInbox(),
    onSettled: () => {
      qc.invalidateQueries({ queryKey: inboxKeys.list(wsId) });
    },
  });
}
