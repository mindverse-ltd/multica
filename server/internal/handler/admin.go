package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	defaultAdminListLimit  = 50
	maxAdminListLimit      = 200
	defaultAdminListOffset = 0
)

type AdminUserResponse struct {
	ID                  string                     `json:"id"`
	Name                string                     `json:"name"`
	Email               string                     `json:"email"`
	AvatarURL           *string                    `json:"avatar_url"`
	OnboardedAt         *string                    `json:"onboarded_at"`
	CreatedAt           string                     `json:"created_at"`
	ExternalIdentities  []AdminExternalIdentityResp `json:"external_identities"`
}

type AdminExternalIdentityResp struct {
	Provider       string  `json:"provider"`
	ProviderUserID string  `json:"provider_user_id"`
	Email          *string `json:"email"`
	Name           *string `json:"name"`
}

type ListAllUsersResponse struct {
	Users []AdminUserResponse `json:"users"`
	Total int64               `json:"total"`
}

type CreateMemberByUserIDRequest struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

func (h *Handler) ListAllUsers(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUserID(w, r)
	if !ok {
		return
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))

	limit := defaultAdminListLimit
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 && l <= maxAdminListLimit {
		limit = l
	}

	offset := defaultAdminListOffset
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}

	users, err := h.Queries.ListAllUsers(r.Context(), db.ListAllUsersParams{
		Search: search,
		Lim:    int32(limit),
		Offs:   int32(offset),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list users")
		return
	}

	total, err := h.Queries.CountAllUsers(r.Context(), search)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count users")
		return
	}

	userIDs := make([]pgtype.UUID, len(users))
	idMap := make(map[string]int, len(users))
	for i, u := range users {
		uid := uuidToString(u.ID)
		userIDs[i] = u.ID
		idMap[uid] = i
	}

	identities, err := h.Queries.ListExternalIdentitiesByUsers(r.Context(), userIDs)
	if err != nil {
		identities = nil
	}

	identityMap := make(map[string][]AdminExternalIdentityResp)
	for _, id := range identities {
		uid := uuidToString(id.UserID)
		identityMap[uid] = append(identityMap[uid], AdminExternalIdentityResp{
			Provider:       id.Provider,
			ProviderUserID: id.ProviderUserID,
			Email:          textToPtr(id.Email),
			Name:           textToPtr(id.Name),
		})
	}

	resp := make([]AdminUserResponse, len(users))
	for i, u := range users {
		uid := uuidToString(u.ID)
		resp[i] = AdminUserResponse{
			ID:                 uid,
			Name:               u.Name,
			Email:              u.Email,
			AvatarURL:          textToPtr(u.AvatarUrl),
			OnboardedAt:        timestampToPtr(u.OnboardedAt),
			CreatedAt:          timestampToString(u.CreatedAt),
			ExternalIdentities: identityMap[uid],
		}
		if resp[i].ExternalIdentities == nil {
			resp[i].ExternalIdentities = []AdminExternalIdentityResp{}
		}
	}

	writeJSON(w, http.StatusOK, ListAllUsersResponse{
		Users: resp,
		Total: total,
	})
}

func (h *Handler) CreateMemberByUserID(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	requester, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}

	if !roleAllowed(requester.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	var req CreateMemberByUserIDRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	role, valid := normalizeMemberRole(req.Role)
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid member role")
		return
	}
	if role == "owner" && requester.Role != "owner" {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	parsedUserID := parseUUID(userID)
	if !parsedUserID.Valid {
		writeError(w, http.StatusBadRequest, "invalid user_id")
		return
	}

	user, err := h.Queries.GetUserByID(r.Context(), parsedUserID)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "user not found")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to look up user")
		}
		return
	}

	existingMember, err := h.Queries.GetMemberByUserAndWorkspace(r.Context(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      user.ID,
		WorkspaceID: parseUUID(workspaceID),
	})
	if err == nil {
		writeJSON(w, http.StatusOK, memberWithUserResponse(existingMember, user))
		return
	}
	if !isNotFound(err) {
		writeError(w, http.StatusInternalServerError, "failed to check existing membership")
		return
	}

	member, err := h.Queries.CreateMember(r.Context(), db.CreateMemberParams{
		WorkspaceID: parseUUID(workspaceID),
		UserID:      user.ID,
		Role:        role,
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeError(w, http.StatusConflict, "user is already a member")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to add member")
		}
		return
	}

	writeJSON(w, http.StatusCreated, memberWithUserResponse(member, user))
}
