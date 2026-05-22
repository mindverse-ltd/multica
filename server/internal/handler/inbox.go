package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type InboxItemResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	RecipientType string          `json:"recipient_type"`
	RecipientID   string          `json:"recipient_id"`
	Type          string          `json:"type"`
	Severity      string          `json:"severity"`
	IssueID       *string         `json:"issue_id"`
	Title         string          `json:"title"`
	Body          *string         `json:"body"`
	Read          bool            `json:"read"`
	Archived      bool            `json:"archived"`
	CreatedAt     string          `json:"created_at"`
	IssueStatus   *string         `json:"issue_status"`
	ActorType     *string         `json:"actor_type"`
	ActorID       *string         `json:"actor_id"`
	Details       json.RawMessage `json:"details"`
}

type InboxPageResponse struct {
	Items      []InboxItemResponse `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

func inboxToResponse(i db.InboxItem) InboxItemResponse {
	return InboxItemResponse{
		ID:            uuidToString(i.ID),
		WorkspaceID:   uuidToString(i.WorkspaceID),
		RecipientType: i.RecipientType,
		RecipientID:   uuidToString(i.RecipientID),
		Type:          i.Type,
		Severity:      i.Severity,
		IssueID:       uuidToPtr(i.IssueID),
		Title:         i.Title,
		Body:          textToPtr(i.Body),
		Read:          i.Read,
		Archived:      i.Archived,
		CreatedAt:     timestampToString(i.CreatedAt),
		ActorType:     textToPtr(i.ActorType),
		ActorID:       uuidToPtr(i.ActorID),
		Details:       json.RawMessage(i.Details),
	}
}

func inboxRowToResponse(r db.ListInboxItemsPageRow) InboxItemResponse {
	return InboxItemResponse{
		ID:            uuidToString(r.ID),
		WorkspaceID:   uuidToString(r.WorkspaceID),
		RecipientType: r.RecipientType,
		RecipientID:   uuidToString(r.RecipientID),
		Type:          r.Type,
		Severity:      r.Severity,
		IssueID:       uuidToPtr(r.IssueID),
		Title:         r.Title,
		Body:          textToPtr(r.Body),
		Read:          r.Read,
		Archived:      r.Archived,
		CreatedAt:     timestampToString(r.CreatedAt),
		IssueStatus:   textToPtr(r.IssueStatus),
		ActorType:     textToPtr(r.ActorType),
		ActorID:       uuidToPtr(r.ActorID),
		Details:       json.RawMessage(r.Details),
	}
}

func (h *Handler) enrichInboxResponse(ctx context.Context, resp InboxItemResponse, issueID pgtype.UUID) InboxItemResponse {
	if !issueID.Valid {
		return resp
	}
	issue, err := h.Queries.GetIssue(ctx, issueID)
	if err == nil {
		s := issue.Status
		resp.IssueStatus = &s
	}
	return resp
}

const (
	defaultInboxPageLimit = 50
	maxInboxPageLimit     = 100
)

func (h *Handler) ListInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	limit := defaultInboxPageLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	if limit > maxInboxPageLimit {
		limit = maxInboxPageLimit
	}

	var beforeCreatedAt pgtype.Timestamptz
	var beforeID pgtype.UUID
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		createdAt, id, ok := parseInboxCursor(w, raw)
		if !ok {
			return
		}
		beforeCreatedAt = pgtype.Timestamptz{Time: createdAt, Valid: true}
		beforeID = id
	}

	rows, err := h.Queries.ListInboxItemsPage(r.Context(), db.ListInboxItemsPageParams{
		WorkspaceID:     wsUUID,
		RecipientType:   "member",
		RecipientID:     parseUUID(userID),
		BeforeCreatedAt: beforeCreatedAt,
		BeforeID:        beforeID,
		Limit:           int32(limit + 1),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list inbox")
		return
	}

	var nextCursor *string
	if len(rows) > limit {
		last := rows[limit-1]
		cursor := formatInboxCursor(last.CreatedAt, last.ID)
		nextCursor = &cursor
		rows = rows[:limit]
	}

	items := make([]InboxItemResponse, len(rows))
	for i, item := range rows {
		items[i] = inboxRowToResponse(item)
	}

	writeJSON(w, http.StatusOK, InboxPageResponse{Items: items, NextCursor: nextCursor})
}

func parseInboxCursor(w http.ResponseWriter, raw string) (time.Time, pgtype.UUID, bool) {
	createdAtRaw, idRaw, found := strings.Cut(raw, "_")
	if !found || createdAtRaw == "" || idRaw == "" {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return time.Time{}, pgtype.UUID{}, false
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return time.Time{}, pgtype.UUID{}, false
	}
	id, ok := parseUUIDOrBadRequest(w, idRaw, "cursor id")
	if !ok {
		return time.Time{}, pgtype.UUID{}, false
	}
	return createdAt, id, true
}

func formatInboxCursor(createdAt pgtype.Timestamptz, id pgtype.UUID) string {
	return createdAt.Time.Format(time.RFC3339Nano) + "_" + uuidToString(id)
}

func (h *Handler) MarkInboxRead(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	prev, ok := h.loadInboxItemForUser(w, r, id)
	if !ok {
		return
	}
	item, err := h.Queries.MarkInboxRead(r.Context(), prev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark read")
		return
	}

	userID := requestUserID(r)
	workspaceID := uuidToString(item.WorkspaceID)
	h.publish(protocol.EventInboxRead, workspaceID, "member", userID, map[string]any{
		"item_id":      uuidToString(item.ID),
		"recipient_id": uuidToString(item.RecipientID),
	})

	resp := h.enrichInboxResponse(r.Context(), inboxToResponse(item), item.IssueID)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ArchiveInboxItem(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	prev, ok := h.loadInboxItemForUser(w, r, id)
	if !ok {
		return
	}
	item, err := h.Queries.ArchiveInboxItem(r.Context(), prev.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive")
		return
	}

	// Archive all sibling inbox items for the same issue (issue-level archive)
	if item.IssueID.Valid {
		h.Queries.ArchiveInboxByIssue(r.Context(), db.ArchiveInboxByIssueParams{
			WorkspaceID:   item.WorkspaceID,
			RecipientType: item.RecipientType,
			RecipientID:   item.RecipientID,
			IssueID:       item.IssueID,
		})
	}

	userID := requestUserID(r)
	workspaceID := uuidToString(item.WorkspaceID)
	h.publish(protocol.EventInboxArchived, workspaceID, "member", userID, map[string]any{
		"item_id":      uuidToString(item.ID),
		"issue_id":     uuidToPtr(item.IssueID),
		"recipient_id": uuidToString(item.RecipientID),
	})

	resp := h.enrichInboxResponse(r.Context(), inboxToResponse(item), item.IssueID)
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) CountUnreadInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.CountUnreadInbox(r.Context(), db.CountUnreadInboxParams{
		WorkspaceID:   wsUUID,
		RecipientType: "member",
		RecipientID:   parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count unread inbox")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{"count": count})
}

func (h *Handler) MarkAllInboxRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.MarkAllInboxRead(r.Context(), db.MarkAllInboxReadParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to mark all inbox read")
		return
	}

	slog.Info("inbox: mark all read", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchRead, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (h *Handler) ArchiveAllInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.ArchiveAllInbox(r.Context(), db.ArchiveAllInboxParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive all inbox")
		return
	}

	slog.Info("inbox: archive all", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchArchived, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (h *Handler) ArchiveAllReadInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.ArchiveAllReadInbox(r.Context(), db.ArchiveAllReadInboxParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive all read inbox")
		return
	}

	slog.Info("inbox: archive all read", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchArchived, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}

func (h *Handler) ArchiveCompletedInbox(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	count, err := h.Queries.ArchiveCompletedInbox(r.Context(), db.ArchiveCompletedInboxParams{
		WorkspaceID: wsUUID,
		RecipientID: parseUUID(userID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive completed inbox")
		return
	}

	slog.Info("inbox: archive completed", append(logger.RequestAttrs(r), "user_id", userID, "count", count)...)
	h.publish(protocol.EventInboxBatchArchived, workspaceID, "member", userID, map[string]any{
		"recipient_id": userID,
		"count":        count,
	})

	writeJSON(w, http.StatusOK, map[string]any{"count": count})
}
