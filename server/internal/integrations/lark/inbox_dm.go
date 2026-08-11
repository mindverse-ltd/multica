package lark

// inbox_dm.go wires the inbox→Feishu DM path: when `inbox:new` fires
// for a user who has bound their Feishu open_id to an installation in
// the workspace, the bot of the agent that triggered the event DMs them
// a short text notification. This closes the "app closed / tab closed /
// screen locked" gap the WS+banner path has — the user receives a real
// Lark DM they can read offline. Zero schema change, zero new Lark scope.
//
// Actor-gated single-DM (MAC-12653): instead of fanning out one DM per
// bound bot (4 bots = 4 identical DMs), we send exactly one DM from the
// bot whose installation belongs to the actor agent. Member and system
// actors have no bot binding, so they are skipped (WS-only).
//
// Severity gate: only `action_required` and `attention` severities
// trigger a DM. `info` stays WS-only to avoid a DM flood — see
// notification_listeners.go for the actor-aware severity assignments.
//
// Mute gate: the user's `system_notifications` preference is honored
// exactly like the OS banner path — when muted, the DM is skipped but
// the in-app unread badge still refreshes (the WS path runs
// independently and is unaffected).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// InboxDMQueries is the narrow queries surface the inbox→Feishu DM
// notifier needs. *ChannelStore satisfies it directly: it embeds
// *db.Queries (which provides ListNotificationPreferencesByUsers) and
// adds the lark-specific installation + binding helpers. Declared as
// an interface so the notifier is unit-testable without a real Postgres
// connection.
type InboxDMQueries interface {
	// ListUserBindingByAgent returns the Feishu binding(s) whose
	// installation belongs to the specified agent — the actor-gated
	// single-DM lookup. The caller MUST re-check installation status
	// before sending.
	ListUserBindingByAgent(ctx context.Context, workspaceID, multicaUserID, agentID pgtype.UUID) ([]UserBinding, error)

	// GetLarkInstallation loads an installation row by id.
	GetLarkInstallation(ctx context.Context, id pgtype.UUID) (Installation, error)

	// ListNotificationPreferencesByUsers loads the notification
	// preference rows for a set of users in one workspace. Used to
	// honor the `system_notifications=muted` toggle.
	ListNotificationPreferencesByUsers(ctx context.Context, arg db.ListNotificationPreferencesByUsersParams) ([]db.NotificationPreference, error)
}

// InboxDMNotifierConfig carries the wiring the notifier needs at boot.
type InboxDMNotifierConfig struct {
	Queries     InboxDMQueries
	Credentials CredentialsResolver
	Client      APIClient
	AppURL      string
	Logger      *slog.Logger
}

// InboxDMNotifier sends a Feishu DM for inbox:new events. Sibling to
// the WS inbox subscriber in cmd/server/listeners.go — both run on the
// same bus, neither blocks the other.
type InboxDMNotifier struct {
	queries     InboxDMQueries
	credentials CredentialsResolver
	client      APIClient
	appURL      string
	log         *slog.Logger
}

// NewInboxDMNotifier constructs the notifier. It does not subscribe to
// the bus until Register is called. Returns nil if any required
// dependency is missing — callers should treat nil as "not wired" and
// skip Register (router.go gates this on larkClient.IsConfigured()).
func NewInboxDMNotifier(cfg InboxDMNotifierConfig) *InboxDMNotifier {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	if cfg.Queries == nil || cfg.Credentials == nil || cfg.Client == nil {
		return nil
	}
	return &InboxDMNotifier{
		queries:     cfg.Queries,
		credentials: cfg.Credentials,
		client:      cfg.Client,
		appURL:      strings.TrimRight(cfg.AppURL, "/"),
		log:         log,
	}
}

// Register subscribes the notifier to inbox:new on the supplied bus.
// Idempotent only against a fresh bus; call sites should invoke it
// exactly once during server boot.
func (n *InboxDMNotifier) Register(bus *events.Bus) {
	if bus == nil {
		return
	}
	bus.Subscribe(protocol.EventInboxNew, n.handleInboxNew)
}

// handleInboxNew sends a Feishu DM for an inbox:new event. Uses a fresh
// background ctx with a tight timeout: the bus delivers synchronously so
// a stuck Lark HTTP call would otherwise wedge the publish call site.
func (n *InboxDMNotifier) handleInboxNew(e events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := n.processInboxNew(ctx, e); err != nil {
		n.log.Warn("lark inbox dm: event handling failed",
			"workspace_id", e.WorkspaceID,
			"error", err,
		)
	}
}

func (n *InboxDMNotifier) processInboxNew(ctx context.Context, e events.Event) error {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return nil
	}
	item, ok := payload["item"].(map[string]any)
	if !ok {
		return nil
	}

	// Only Multica members can be Feishu-bound; agents and "all"
	// recipients have no open_id. Skip silently — the WS path still
	// delivers.
	recipientType, _ := item["recipient_type"].(string)
	if recipientType != "member" {
		return nil
	}
	recipientID, _ := item["recipient_id"].(string)
	if recipientID == "" {
		return nil
	}
	severity, _ := item["severity"].(string)
	if !shouldDMSeverity(severity) {
		return nil
	}

	// Actor-gated single-DM (MAC-12653): only agent actors have a bot
	// binding. Member and system actors are skipped — the WS path still
	// delivers.
	actorType, _ := item["actor_type"].(*string)
	if actorType == nil || *actorType != "agent" {
		return nil
	}
	actorID, _ := item["actor_id"].(*string)
	if actorID == nil || *actorID == "" {
		return nil
	}

	workspaceID, err := util.ParseUUID(e.WorkspaceID)
	if err != nil {
		return nil
	}
	recipientUUID, err := util.ParseUUID(recipientID)
	if err != nil {
		return nil
	}
	actorUUID, err := util.ParseUUID(*actorID)
	if err != nil {
		return nil
	}

	// Honor `system_notifications=muted` exactly like the OS banner
	// client does. Mute here means "no DM", but the WS path still
	// refreshes the in-app badge — the user muted push, not the inbox.
	if n.systemNotificationsMuted(ctx, workspaceID, recipientUUID) {
		return nil
	}

	// Actor-gated lookup: get the binding(s) whose installation belongs
	// to the actor agent. This replaces the old fan-out — the user gets
	// one DM from the bot of the agent that did the work, not one DM
	// per bound bot.
	bindings, err := n.queries.ListUserBindingByAgent(ctx, workspaceID, recipientUUID, actorUUID)
	if err != nil {
		return fmt.Errorf("list user binding by agent: %w", err)
	}
	if len(bindings) == 0 {
		// The actor agent's bot is either not installed or not bound to
		// this user — fall back to WS+banner. Not an error.
		return nil
	}

	text := renderInboxDMText(item, n.appURL)

	for _, b := range bindings {
		if err := n.sendDM(ctx, b, text); err != nil {
			n.log.Warn("lark inbox dm: send failed",
				"workspace_id", e.WorkspaceID,
				"installation_id", util.UUIDToString(b.InstallationID),
				"open_id", b.ChannelUserID,
				"error", err,
			)
			continue
		}
	}
	return nil
}

func (n *InboxDMNotifier) sendDM(ctx context.Context, b UserBinding, text string) error {
	inst, err := n.queries.GetLarkInstallation(ctx, b.InstallationID)
	if err != nil {
		return fmt.Errorf("load installation: %w", err)
	}
	if InstallationStatus(inst.Status) != InstallationActive {
		// Revoked between bind and now; skip silently.
		n.log.Warn("lark inbox dm: actor agent bot revoked, skipping DM",
			"workspace_id", util.UUIDToString(inst.WorkspaceID),
			"installation_id", util.UUIDToString(b.InstallationID),
			"agent_id", util.UUIDToString(inst.AgentID),
		)
		return nil
	}
	secret, err := n.credentials.DecryptAppSecret(inst)
	if err != nil {
		return fmt.Errorf("decrypt app_secret: %w", err)
	}
	creds := InstallationCredentials{
		AppID:     inst.AppID,
		AppSecret: secret,
		Region:    RegionOrDefault(inst.Region),
	}
	if inst.TenantKey.Valid {
		creds.TenantKey = inst.TenantKey.String
	}
	return n.client.SendDirectMessage(ctx, DirectMessageParams{
		InstallationID: creds,
		OpenID:         OpenID(b.ChannelUserID),
		Text:           text,
	})
}

// systemNotificationsMuted loads the user's notification preferences and
// reports whether the `system_notifications` channel toggle is muted.
// A missing preference row means "not muted" — defaults to opted in.
// A JSON-decode failure is treated as not-muted rather than blocking the
// DM, mirroring loadUserPrefs in cmd/server/notification_listeners.go.
func (n *InboxDMNotifier) systemNotificationsMuted(ctx context.Context, workspaceID, userID pgtype.UUID) bool {
	rows, err := n.queries.ListNotificationPreferencesByUsers(ctx, db.ListNotificationPreferencesByUsersParams{
		WorkspaceID: workspaceID,
		UserIds:     []pgtype.UUID{userID},
	})
	if err != nil {
		n.log.Warn("lark inbox dm: load notification preferences failed",
			"workspace_id", util.UUIDToString(workspaceID),
			"user_id", util.UUIDToString(userID),
			"error", err,
		)
		return false
	}
	for _, row := range rows {
		var prefs map[string]string
		if err := json.Unmarshal(row.Preferences, &prefs); err != nil {
			continue
		}
		return prefs["system_notifications"] == "muted"
	}
	return false
}

// shouldDMSeverity reports whether a severity level should trigger a
// Feishu DM. Conservative default: only action_required and attention.
// info (every comment, every status flip) stays WS-only.
func shouldDMSeverity(severity string) bool {
	switch severity {
	case "action_required", "attention":
		return true
	default:
		return false
	}
}

// renderInboxDMText builds the DM body from the inbox item. Format:
//
//	[<title>]
//	<context line — comment body / status / priority>
//	<issue_url>
//
// The context line is type-specific:
//   - new_comment: full comment body (truncated to the Lark API limit only)
//   - status_changed: "状态 → <to_status>"
//   - priority_changed: "优先级 → <new_priority>"
//   - other types: no context line (title + URL only)
//
// When the issue id is missing, the URL line is omitted.
//
// The final rendered text is capped to the Lark text-message byte limit
// (approximately 4 KB) so an extremely long comment does not exceed the
// API ceiling. This is a safety net — most comments fit in full.
func renderInboxDMText(item map[string]any, appURL string) string {
	title, _ := item["title"].(string)
	if title == "" {
		title = "Inbox update"
	}

	var b strings.Builder
	b.WriteString("[")
	b.WriteString(title)
	b.WriteString("]")

	contextLine := renderInboxDMContext(item)
	if contextLine != "" {
		b.WriteString("\n")
		b.WriteString(contextLine)
	}

	issueID, _ := item["issue_id"].(*string)
	if issueID != nil && *issueID != "" && appURL != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(appURL, "/"))
		b.WriteString("/issues/")
		b.WriteString(*issueID)
	}

	return truncateLarkText(b.String())
}

// renderInboxDMContext produces the type-specific context line for the
// DM body. Returns "" when the item type has no rich context (or the
// relevant field is missing), in which case the DM falls back to
// title + URL.
func renderInboxDMContext(item map[string]any) string {
	itemType, _ := item["type"].(string)
	switch itemType {
	case "new_comment":
		body, _ := item["body"].(*string)
		if body == nil || *body == "" {
			return ""
		}
		return *body
	case "status_changed":
		details, _ := item["details"].(map[string]any)
		if details == nil {
			return ""
		}
		to, _ := details["to"].(string)
		if to == "" {
			return ""
		}
		return "状态 → " + to
	case "priority_changed":
		details, _ := item["details"].(map[string]any)
		if details == nil {
			return ""
		}
		newPriority, _ := details["to"].(string)
		if newPriority == "" {
			return ""
		}
		return "优先级 → " + newPriority
	default:
		return ""
	}
}

// maxLarkTextLen is the practical byte ceiling for a Lark text-type
// message. The Lark API documents ~4 KB for text messages; we use a
// conservative 4000-byte cap to stay safely under the limit.
const maxLarkTextLen = 4000

// truncateLarkText caps s to maxLarkTextLen bytes on a rune boundary,
// appending "…" when truncation occurred. This is a safety net for the
// final rendered DM so an extremely long comment body does not exceed
// the Lark text-message API ceiling.
func truncateLarkText(s string) string {
	if len(s) <= maxLarkTextLen {
		return s
	}
	// "…" is 3 bytes (U+2026). Reserve room for it so the final string
	// does not exceed maxLarkTextLen, then walk back to a rune boundary
	// so we never split a multi-byte character.
	end := maxLarkTextLen - 3 // leave room for "…"
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "…"
}
