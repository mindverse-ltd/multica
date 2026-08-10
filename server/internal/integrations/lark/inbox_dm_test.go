package lark

// inbox_dm_test.go covers the inbox:new → Feishu DM fan-out. Three
// contracts are pinned:
//   - Happy path: a bound member recipient with an active installation
//     and action_required/attention severity triggers exactly one
//     SendDirectMessage per binding, with the issue URL rendered.
//   - Mute skip: a recipient with `system_notifications=muted` is NOT
//     DMed, even when severity would otherwise pass.
//   - Info skip: an `info`-severity inbox item is NOT DMed (avoid
//     flooding the user with comment/status noise).

import (
	"context"
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

func (f *fakeInboxDMQueries) ListUserBindingsByMulticaUser(ctx context.Context, workspaceID, multicaUserID pgtype.UUID) ([]UserBinding, error) {
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
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("info severity should not DM, got %d calls: %+v", got, c.dmCalls)
	}
}

func TestInboxDMNotifier_NoBindingNoDM(t *testing.T) {
	const (
		wsID   = "00000000-0000-4000-8000-0000000000a1"
		userID = "00000000-0000-4000-8000-0000000000b2"
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
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("unbound user should not be DMed, got %d", got)
	}
}

func TestInboxDMNotifier_RevokedInstallationSkip(t *testing.T) {
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
		}},
	})

	if got := len(c.dmCalls); got != 0 {
		t.Fatalf("revoked installation should not DM, got %d", got)
	}
}

func TestRenderInboxDMText(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		issueID *string
		appURL  string
		want    string
	}{
		{
			name:    "title and issue id",
			title:   "Hello",
			issueID: strPtr("abc-123"),
			appURL:  "https://multica.test",
			want:    "[Hello]\nhttps://multica.test/issues/abc-123",
		},
		{
			name:    "no issue id falls back to title only",
			title:   "Hello",
			issueID: nil,
			appURL:  "https://multica.test",
			want:    "[Hello]",
		},
		{
			name:    "empty title defaults",
			title:   "",
			issueID: strPtr("abc"),
			appURL:  "https://multica.test",
			want:    "[Inbox update]\nhttps://multica.test/issues/abc",
		},
		{
			name:    "trailing slash in app url is trimmed",
			title:   "Hi",
			issueID: strPtr("abc"),
			appURL:  "https://multica.test/",
			want:    "[Hi]\nhttps://multica.test/issues/abc",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderInboxDMText(tc.title, tc.issueID, tc.appURL)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
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
// payload carries for issue_id without repeating the cast.
func strPtr(s string) *string { return &s }
