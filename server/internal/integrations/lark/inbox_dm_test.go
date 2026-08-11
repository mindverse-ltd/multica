package lark

// inbox_dm_test.go covers the inbox:new → Feishu DM actor-gated path
// (MAC-12653). Three contracts are pinned:
//   - Happy path: a bound member recipient with an agent actor and an
//     active installation triggers exactly one SendDirectMessage, with
//     the issue URL rendered.
//   - Mute skip: a recipient with `system_notifications=muted` is NOT
//     DMed, even when severity would otherwise pass.
//   - Info skip: an `info`-severity inbox item is NOT DMed (avoid
//     flooding the user with comment/status noise).
//   - Member actor skip: a member-authored event does NOT trigger a DM
//     (no bot binding for member actors).
//   - Revoked installation skip: the actor agent's bot is revoked → no
//     DM, logged as warn.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
)

// fakeInboxDMQueries is a stub InboxDMQueries used by the DM notifier
// tests. It returns canned bindings + installations and records the
// preference lookups so tests can assert mute behavior.
type fakeInboxDMQueries struct {
	mu          sync.Mutex
	bindings    []UserBinding
	installByID map[string]Installation
	prefs       map[string][]byte // user_id string -> preferences JSON

	prefCalls int
}

func (f *fakeInboxDMQueries) ListUserBindingByAgent(ctx context.Context, workspaceID, multicaUserID, agentID pgtype.UUID) ([]UserBinding, error) {
	return f.bindings, nil
}

func (f *fakeInboxDMQueries) GetLarkInstallation(ctx context.Context, id pgtype.UUID) (Installation, error) {
	if inst, ok := f.installByID[util.UUIDToString(id)]; ok {
		return inst, nil
	}
	return Installation{}, nil
}

func (f *fakeInboxDMQueries) ListNotificationPreferencesByUsers(ctx context.Context, arg db.ListNotificationPreferencesByUsersParams) ([]db.NotificationPreference, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prefCalls++
	var out []db.NotificationPreference
	for _, uid := range arg.UserIds {
		if prefs, ok := f.prefs[util.UUIDToString(uid)]; ok {
			out = append(out, db.NotificationPreference{
				WorkspaceID: arg.WorkspaceID,
				UserID:      uid,
				Preferences: prefs,
			})
		}
	}
	return out, nil
}

// recordingDMClient is a minimal APIClient that only records
// SendDirectMessage calls. The other methods panic so a stray call
// surfaces loudly rather than silently passing.
type recordingDMClient struct {
	mu      sync.Mutex
	dmCalls []DirectMessageParams
}

func (c *recordingDMClient) IsConfigured() bool { return true }
func (c *recordingDMClient) SendDirectMessage(ctx context.Context, p DirectMessageParams) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dmCalls = append(c.dmCalls, p)
	return nil
}
func (c *recordingDMClient) SendInteractiveCard(context.Context, SendCardParams) (string, error) {
	panic("SendInteractiveCard should not be called on the DM test client")
}
func (c *recordingDMClient) PatchInteractiveCard(context.Context, PatchCardParams) error {
	panic("PatchInteractiveCard should not be called on the DM test client")
}
func (c *recordingDMClient) SendTextMessage(context.Context, SendTextParams) (string, error) {
	panic("SendTextMessage should not be called on the DM test client")
}
func (c *recordingDMClient) SendMarkdownCard(context.Context, SendMarkdownCardParams) (string, error) {
	panic("SendMarkdownCard should not be called on the DM test client")
}
func (c *recordingDMClient) SendBindingPromptCard(context.Context, BindingPromptParams) error {
	panic("SendBindingPromptCard should not be called on the DM test client")
}
func (c *recordingDMClient) GetBotInfo(context.Context, InstallationCredentials) (BotInfo, error) {
	panic("GetBotInfo should not be called on the DM test client")
}
func (c *recordingDMClient) GetMessage(context.Context, InstallationCredentials, string) ([]LarkMessage, error) {
	panic("GetMessage should not be called on the DM test client")
}
func (c *recordingDMClient) ListChatMessages(context.Context, InstallationCredentials, ListMessagesParams) ([]LarkMessage, error) {
	panic("ListChatMessages should not be called on the DM test client")
}
func (c *recordingDMClient) DownloadMessageResource(context.Context, InstallationCredentials, DownloadResourceParams) (DownloadedResource, error) {
	panic("DownloadMessageResource should not be called on the DM test client")
}
func (c *recordingDMClient) BatchGetUsers(context.Context, InstallationCredentials, []string) (map[string]string, error) {
	panic("BatchGetUsers should not be called on the DM test client")
}
func (c *recordingDMClient) AddMessageReaction(context.Context, AddReactionParams) (string, error) {
	panic("AddMessageReaction should not be called on the DM test client")
}
func (c *recordingDMClient) DeleteMessageReaction(context.Context, DeleteReactionParams) error {
	panic("DeleteMessageReaction should not be called on the DM test client")
}

// newInboxDMTestNotifier wires a notifier against the supplied fakes.
// AppURL is fixed so the URL rendering is deterministic.
func newInboxDMTestNotifier(t *testing.T, q *fakeInboxDMQueries, c *recordingDMClient) *InboxDMNotifier {
	t.Helper()
	n := NewInboxDMNotifier(InboxDMNotifierConfig{
		Queries:     q,
		Credentials: fakeCredentials{secret: "secret"},
		Client:      c,
		AppURL:      "https://multica.test",
	})
	if n == nil {
		t.Fatalf("NewInboxDMNotifier returned nil")
	}
	return n
}

func TestInboxDMNotifier_HappyPath(t *testing.T) {
	const (
		wsID    = "00000000-0000-4000-8000-0000000000a1"
		userID  = "00000000-0000-4000-8000-0000000000b2"
		instID  = "00000000-0000-4000-8000-0000000000c3"
		agentID = "00000000-0000-4000-8000-0000000000f6"
		issueID = "00000000-0000-4000-8000-0000000000d4"
		openID  = "ou_user_happy"
	)
	q := &fakeInboxDMQueries{
		bindings: []UserBinding{
			{
				ID:             util.MustParseUUID("00000000-0000-4000-8000-0000000000e5"),
				WorkspaceID:    util.MustParseUUID(wsID),
				MulticaUserID:  util.MustParseUUID(userID),
				InstallationID: util.MustParseUUID(instID),
				ChannelUserID:  openID,
			},
		},
		installByID: map[string]Installation{
			instID: {
				ID:     util.MustParseUUID(instID),
				Status: string(InstallationActive),
				AppID:  "cli_happy",
				Region: "",
			},
		},
	}
	c := &recordingDMClient{}
	n := newInboxDMTestNotifier(t, q, c)
	bus := events.New()
	n.Register(bus)

	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: wsID,
		Payload: map[string]any{"item": map[string]any{
			"recipient_type": "member",
			"recipient_id":   userID,
			"severity":       "attention",
			"type":           "mentioned",
			"title":          "Review this PR",
			"issue_id":       strPtr(issueID),
			"actor_type":     strPtr("agent"),
			"actor_id":       strPtr(agentID),
		}},
	})

	if got := len(c.dmCalls); got != 1 {
		t.Fatalf("want 1 DM, got %d", got)
	}
	dm := c.dmCalls[0]
	if string(dm.OpenID) != openID {
		t.Errorf("open_id: got %q want %q", dm.OpenID, openID)
	}
	if dm.InstallationID.AppID != "cli_happy" {
		t.Errorf("app_id: got %q", dm.InstallationID.AppID)
	}
	if dm.InstallationID.AppSecret != "secret" {
		t.Errorf("app_secret: got %q", dm.InstallationID.AppSecret)
	}
	if !contains(dm.Text, "Review this PR") {
		t.Errorf("text should carry title: %q", dm.Text)
	}
	if !contains(dm.Text, "multica.test/issues/"+issueID) {
		t.Errorf("text should carry issue URL: %q", dm.Text)
	}
}

func TestInboxDMNotifier_MutedSkip(t *testing.T) {
	const (
		wsID    = "00000000-0000-4000-8000-0000000000a1"
		userID  = "00000000-0000-4000-8000-0000000000b2"
		instID  = "00000000-0000-4000-8000-0000000000c3"
		agentID = "00000000-0000-4000-8000-0000000000f6"
	)
	q := &fakeInboxDMQueries{
		bindings: []UserBinding{
			{
				WorkspaceID:    util.MustParseUUID(wsID),
				MulticaUserID:  util.MustParseUUID(userID),
				InstallationID: util.MustParseUUID(instID),
				ChannelUserID:  "ou_user_muted",
			},
		},
		installByID: map[string]Installation{
			instID: {ID: util.MustParseUUID(instID), Status: string(InstallationActive)},
		},
		prefs: map[string][]byte{
			userID: []byte(`{"system_notifications":"muted"}`),
		},
	}
	c := &recordingDMClient{}
	n := newInboxDMTestNotifier(t, q, c)
	bus := events.New()
	n.Register(bus)

	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: wsID,
		Payload: map[string]any{"item": map[string]any{
			"recipient_type": "member",
			"recipient_id":   userID,
			"severity":       "attention",
			"title":          "Should not DM",
			"issue_id":       strPtr("00000000-0000-4000-8000-0000000000d4"),
			"actor_type":     strPtr("agent"),
			"actor_id":       strPtr(agentID),
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("muted user should not be DMed, got %d calls: %+v", got, c.dmCalls)
	}
	if q.prefCalls == 0 {
		t.Errorf("expected the mute gate to actually consult preferences")
	}
}

func TestInboxDMNotifier_InfoSeveritySkip(t *testing.T) {
	const (
		wsID    = "00000000-0000-4000-8000-0000000000a1"
		userID  = "00000000-0000-4000-8000-0000000000b2"
		instID  = "00000000-0000-4000-8000-0000000000c3"
		agentID = "00000000-0000-4000-8000-0000000000f6"
	)
	q := &fakeInboxDMQueries{
		bindings: []UserBinding{
			{
				WorkspaceID:    util.MustParseUUID(wsID),
				MulticaUserID:  util.MustParseUUID(userID),
				InstallationID: util.MustParseUUID(instID),
				ChannelUserID:  "ou_user_info",
			},
		},
		installByID: map[string]Installation{
			instID: {ID: util.MustParseUUID(instID), Status: string(InstallationActive)},
		},
	}
	c := &recordingDMClient{}
	n := newInboxDMTestNotifier(t, q, c)
	bus := events.New()
	n.Register(bus)

	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: wsID,
		Payload: map[string]any{"item": map[string]any{
			"recipient_type": "member",
			"recipient_id":   userID,
			"severity":       "info",
			"type":           "new_comment",
			"title":          "someone commented",
			"issue_id":       strPtr("00000000-0000-4000-8000-0000000000d4"),
			"actor_type":     strPtr("agent"),
			"actor_id":       strPtr(agentID),
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("info severity should not DM, got %d calls: %+v", got, c.dmCalls)
	}
}

func TestInboxDMNotifier_NoBindingNoDM(t *testing.T) {
	const (
		wsID    = "00000000-0000-4000-8000-0000000000a1"
		userID  = "00000000-0000-4000-8000-0000000000b2"
		agentID = "00000000-0000-4000-8000-0000000000f6"
	)
	q := &fakeInboxDMQueries{
		bindings:    nil, // unbound
		installByID: map[string]Installation{},
	}
	c := &recordingDMClient{}
	n := newInboxDMTestNotifier(t, q, c)
	bus := events.New()
	n.Register(bus)

	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: wsID,
		Payload: map[string]any{"item": map[string]any{
			"recipient_type": "member",
			"recipient_id":   userID,
			"severity":       "action_required",
			"title":          "x",
			"actor_type":     strPtr("agent"),
			"actor_id":       strPtr(agentID),
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("unbound user should not be DMed, got %d", got)
	}
}

func TestInboxDMNotifier_RevokedInstallationSkip(t *testing.T) {
	const (
		wsID    = "00000000-0000-4000-8000-0000000000a1"
		userID  = "00000000-0000-4000-8000-0000000000b2"
		instID  = "00000000-0000-4000-8000-0000000000c3"
		agentID = "00000000-0000-4000-8000-0000000000f6"
	)
	q := &fakeInboxDMQueries{
		bindings: []UserBinding{
			{
				WorkspaceID:    util.MustParseUUID(wsID),
				MulticaUserID:  util.MustParseUUID(userID),
				InstallationID: util.MustParseUUID(instID),
				ChannelUserID:  "ou_user_revoked",
			},
		},
		installByID: map[string]Installation{
			instID: {ID: util.MustParseUUID(instID), Status: "revoked"},
		},
	}
	c := &recordingDMClient{}
	n := newInboxDMTestNotifier(t, q, c)
	bus := events.New()
	n.Register(bus)

	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: wsID,
		Payload: map[string]any{"item": map[string]any{
			"recipient_type": "member",
			"recipient_id":   userID,
			"severity":       "attention",
			"title":          "x",
			"issue_id":       strPtr("00000000-0000-4000-8000-0000000000d4"),
			"actor_type":     strPtr("agent"),
			"actor_id":       strPtr(agentID),
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("revoked installation should not DM, got %d", got)
	}
}

func TestInboxDMNotifier_MemberActorNoDM(t *testing.T) {
	const (
		wsID   = "00000000-0000-4000-8000-0000000000a1"
		userID = "00000000-0000-4000-8000-0000000000b2"
		instID = "00000000-0000-4000-8000-0000000000c3"
	)
	q := &fakeInboxDMQueries{
		bindings: []UserBinding{
			{
				WorkspaceID:    util.MustParseUUID(wsID),
				MulticaUserID:  util.MustParseUUID(userID),
				InstallationID: util.MustParseUUID(instID),
				ChannelUserID:  "ou_member_actor",
			},
		},
		installByID: map[string]Installation{
			instID: {ID: util.MustParseUUID(instID), Status: string(InstallationActive)},
		},
	}
	c := &recordingDMClient{}
	n := newInboxDMTestNotifier(t, q, c)
	bus := events.New()
	n.Register(bus)

	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: wsID,
		Payload: map[string]any{"item": map[string]any{
			"recipient_type": "member",
			"recipient_id":   userID,
			"severity":       "attention",
			"title":          "member commented",
			"issue_id":       strPtr("00000000-0000-4000-8000-0000000000d4"),
			"actor_type":     strPtr("member"),
			"actor_id":       strPtr(userID),
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("member actor should not DM, got %d", got)
	}
}

func TestInboxDMNotifier_SystemActorNoDM(t *testing.T) {
	const (
		wsID   = "00000000-0000-4000-8000-0000000000a1"
		userID = "00000000-0000-4000-8000-0000000000b2"
		instID = "00000000-0000-4000-8000-0000000000c3"
	)
	q := &fakeInboxDMQueries{
		bindings: []UserBinding{
			{
				WorkspaceID:    util.MustParseUUID(wsID),
				MulticaUserID:  util.MustParseUUID(userID),
				InstallationID: util.MustParseUUID(instID),
				ChannelUserID:  "ou_system_actor",
			},
		},
		installByID: map[string]Installation{
			instID: {ID: util.MustParseUUID(instID), Status: string(InstallationActive)},
		},
	}
	c := &recordingDMClient{}
	n := newInboxDMTestNotifier(t, q, c)
	bus := events.New()
	n.Register(bus)

	bus.Publish(events.Event{
		Type:        protocol.EventInboxNew,
		WorkspaceID: wsID,
		Payload: map[string]any{"item": map[string]any{
			"recipient_type": "member",
			"recipient_id":   userID,
			"severity":       "attention",
			"title":          "system event",
			"issue_id":       strPtr("00000000-0000-4000-8000-0000000000d4"),
			"actor_type":     strPtr("system"),
			"actor_id":       strPtr(""),
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("system actor should not DM, got %d", got)
	}
}

func TestRenderInboxDMText_NewComment(t *testing.T) {
	item := map[string]any{
		"type":     "new_comment",
		"title":    "Agent finished work",
		"body":     strPtr("I completed the refactoring. All tests pass."),
		"issue_id": strPtr("abc-123"),
	}
	got := renderInboxDMText(item, "https://multica.test")
	want := "[Agent finished work]\nI completed the refactoring. All tests pass.\nhttps://multica.test/issues/abc-123"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRenderInboxDMText_NewCommentFullBody(t *testing.T) {
	// A comment shorter than the Lark API limit should be included in
	// full — no 200-rune truncation.
	longBody := make([]rune, 300)
	for i := range longBody {
		longBody[i] = 'x'
	}
	item := map[string]any{
		"type":     "new_comment",
		"title":    "T",
		"body":     strPtr(string(longBody)),
		"issue_id": strPtr("abc"),
	}
	got := renderInboxDMText(item, "https://multica.test")
	lines := splitLines(got)
	if len(lines) < 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), got)
	}
	if len([]rune(lines[1])) != 300 {
		t.Errorf("expected full 300 runes in body line, got %d: %q", len([]rune(lines[1])), lines[1])
	}
}

func TestRenderInboxDMText_NewCommentLarkLimitTruncation(t *testing.T) {
	// An extremely long comment that exceeds the Lark text-message byte
	// limit is truncated at the API ceiling (not at 200 runes), with a
	// trailing ellipsis. The body must still fit under maxLarkTextLen.
	oversized := make([]rune, 5000) // 5000 ASCII runes = 5000 bytes > 4000 cap
	for i := range oversized {
		oversized[i] = 'x'
	}
	item := map[string]any{
		"type":     "new_comment",
		"title":    "T",
		"body":     strPtr(string(oversized)),
		"issue_id": strPtr("abc"),
	}
	got := renderInboxDMText(item, "https://multica.test")
	if len(got) > maxLarkTextLen {
		t.Fatalf("rendered text %d bytes exceeds Lark limit %d", len(got), maxLarkTextLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected trailing ellipsis after truncation, got suffix %q", got[max(len(got)-10, 0):])
	}
}

func TestRenderInboxDMText_StatusChanged(t *testing.T) {
	item := map[string]any{
		"type":     "status_changed",
		"title":    "Status update",
		"issue_id": strPtr("abc-123"),
		"details":  map[string]any{"from": "in_progress", "to": "in_review"},
	}
	got := renderInboxDMText(item, "https://multica.test")
	want := "[Status update]\n状态 → in_review\nhttps://multica.test/issues/abc-123"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRenderInboxDMText_PriorityChanged(t *testing.T) {
	item := map[string]any{
		"type":     "priority_changed",
		"title":    "Priority bump",
		"issue_id": strPtr("abc-123"),
		"details":  map[string]any{"from": "medium", "to": "urgent"},
	}
	got := renderInboxDMText(item, "https://multica.test")
	want := "[Priority bump]\n优先级 → urgent\nhttps://multica.test/issues/abc-123"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRenderInboxDMText_GenericType(t *testing.T) {
	item := map[string]any{
		"type":     "mentioned",
		"title":    "You were mentioned",
		"issue_id": strPtr("abc-123"),
	}
	got := renderInboxDMText(item, "https://multica.test")
	want := "[You were mentioned]\nhttps://multica.test/issues/abc-123"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRenderInboxDMText_NoIssueID(t *testing.T) {
	item := map[string]any{
		"type":  "mentioned",
		"title": "Hi",
	}
	got := renderInboxDMText(item, "https://multica.test")
	want := "[Hi]"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRenderInboxDMText_EmptyTitle(t *testing.T) {
	item := map[string]any{
		"type":     "mentioned",
		"title":    "",
		"issue_id": strPtr("abc"),
	}
	got := renderInboxDMText(item, "https://multica.test")
	want := "[Inbox update]\nhttps://multica.test/issues/abc"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestShouldDMSeverity(t *testing.T) {
	if !shouldDMSeverity("action_required") {
		t.Errorf("action_required should pass")
	}
	if !shouldDMSeverity("attention") {
		t.Errorf("attention should pass")
	}
	if shouldDMSeverity("info") {
		t.Errorf("info should not pass")
	}
	if shouldDMSeverity("") {
		t.Errorf("empty should not pass")
	}
}

// strPtr is a tiny helper so tests can build the *string the inbox
// payload carries for issue_id/body/actor fields without repeating the cast.
func strPtr(s string) *string { return &s }

// splitLines splits a string on \n. Test helper for asserting multi-line
// DM body content.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i, r := range s {
		if r == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}
