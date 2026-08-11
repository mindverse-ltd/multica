package lark

// inbox_dm.go wires the inbox→Feishu DM fan-out: when `inbox:new` fires
// for a user who has bound their Feishu open_id to one or more
// installations in the workspace, the bound bot(s) DM them a short text
// notification. This closes the "app closed / tab closed / screen
// locked" gap the WS+banner path has — the user receives a real Lark
// DM they can read offline. Zero schema change, zero new Lark scope.
//
// Severity gate: only `action_required` and `attention` severities fan
// out. `info` (every comment, routine status flips) stays WS-only to
// avoid a DM flood — see notification_listeners.go for the per-call-site
// promotions that make `mentioned` / `unassigned` reach this gate, and
// severityForStatusChange, which promotes a status_changed into
// in_review / done / cancelled / blocked to `attention` so those
// "needs a human" transitions DM the recipient.
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
	// ListUserBindingsByMulticaUser returns every Feishu binding the
	// user has across all installations in the workspace. The caller
	// MUST re-check each installation's status before sending.
	ListUserBindingsByMulticaUser(ctx context.Context, workspaceID, multicaUserID pgtype.UUID) ([]UserBinding, error)

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

// InboxDMNotifier fans inbox:new events out to Feishu DMs. Sibling to
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

// handleInboxNew fans an inbox:new event out to every Feishu binding the
// recipient has. Uses a fresh background ctx with a tight timeout: the
// bus delivers synchronously so a stuck Lark HTTP call would otherwise
// wedge the publish call site.
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

	workspaceID, err := util.ParseUUID(e.WorkspaceID)
	if err != nil {
		return nil
	}
	recipientUUID, err := util.ParseUUID(recipientID)
	if err != nil {
		return nil
	}

	// Honor `system_notifications=muted` exactly like the OS banner
	// client does. Mute here means "no DM", but the WS path still
	// refreshes the in-app badge — the user muted push, not the inbox.
	if n.systemNotificationsMuted(ctx, workspaceID, recipientUUID) {
		return nil
	}

	bindings, err := n.queries.ListUserBindingsByMulticaUser(ctx, workspaceID, recipientUUID)
	if err != nil {
		return fmt.Errorf("list user bindings: %w", err)
	}
	if len(bindings) == 0 {
		// Unbound user — fall back to WS+banner. Not an error.
		return nil
	}

	title, _ := item["title"].(string)
	issueID, _ := item["issue_id"].(*string)
	text := renderInboxDMText(title, issueID, n.appURL)

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
// info (every comment, routine status flips) stays WS-only; a
// status_changed that reaches in_review / done / cancelled / blocked is
// promoted to attention upstream (see notification_listeners.go).
func shouldDMSeverity(severity string) bool {
	switch severity {
	case "action_required", "attention":
		return true
	default:
		return false
	}
}

// renderInboxDMText builds the DM body. Format:
//
//	[<title>]
//	<issue_url>
//
// When the issue id is missing (rare — inbox items always carry one
// today, but defensively), the URL line is omitted.
func renderInboxDMText(title string, issueID *string, appURL string) string {
	if title == "" {
		title = "Inbox update"
	}
	if issueID != nil && *issueID != "" && appURL != "" {
		return "[" + title + "]\n" + strings.TrimRight(appURL, "/") + "/issues/" + *issueID
	}
	return "[" + title + "]"
}
