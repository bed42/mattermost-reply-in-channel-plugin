package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Fixture identities
const (
	echoID  = "echo1"  // channel-level /ric echo post
	rootID  = "root1"  // original thread root the echo post refers to
	replyID = "reply1" // reply typed into the stray thread under the echo post

	chanID = "chan1"
	teamID = "team1"

	echoAuthor = "alice" // who ran /ric
	rootAuthor = "bob"   // original thread author
	replier    = "carol" // typed into the stray thread

	siteURL  = "http://localhost:8065"
	teamName = "myteam"
)

// newEchoPost is a /ric channel echo post linked to the original thread root.
// Fresh instance per call: tests and the plugin mutate posts in place.
func newEchoPost() *model.Post {
	post := &model.Post{Id: echoID, UserId: echoAuthor, ChannelId: chanID, Message: "echo text"}
	post.AddProp(ricThreadRootIDProp, rootID)
	return post
}

func newOriginalRoot() *model.Post {
	return &model.Post{Id: rootID, UserId: rootAuthor, ChannelId: chanID, Message: "the real discussion"}
}

// newStrayReply is a reply typed into the stray thread hanging off the echo post.
func newStrayReply() *model.Post {
	return &model.Post{
		Id:        replyID,
		UserId:    replier,
		ChannelId: chanID,
		RootId:    echoID,
		Message:   "agreed!",
	}
}

// permalinkFor is the permalink buildPermalink produces for a fixture post.
func permalinkFor(postID string) string {
	return fmt.Sprintf("%s/%s/pl/%s", siteURL, teamName, postID)
}

// emptyThread mirrors what GetPostThread returns for an echo post with no
// replies yet: the root itself, and nothing else.
func emptyThread(root *model.Post) *model.PostList {
	return &model.PostList{Posts: map[string]*model.Post{root.Id: root}, Order: []string{root.Id}}
}

// threadWithLegacyReply mirrors an echo thread that already has a reply,
// as only pre-version or plugin-disabled posts can produce.
func threadWithLegacyReply(root *model.Post) *model.PostList {
	legacy := &model.Post{Id: "legacy1", UserId: replier, ChannelId: chanID, RootId: root.Id, Message: "typed before 1.1.0"}
	return &model.PostList{
		Posts: map[string]*model.Post{root.Id: root, legacy.Id: legacy},
		Order: []string{root.Id, legacy.Id},
	}
}

// testConfig returns a config with SiteURL set so buildPermalink works.
func testConfig() *model.Config {
	config := model.Config{}
	config.ServiceSettings.SiteURL = model.NewPointer(siteURL)
	return &config
}

func testChannel() *model.Channel {
	return &model.Channel{Id: chanID, TeamId: teamID, Name: "town-square", Type: model.ChannelTypeOpen}
}

func testTeam() *model.Team {
	return &model.Team{Id: teamID, Name: teamName}
}

// expectPermalinkEnv registers the lookups buildPermalink performs.
func expectPermalinkEnv(api *plugintest.API) {
	api.On("GetConfig").Return(testConfig())
	api.On("GetChannel", chanID).Return(testChannel(), nil)
	api.On("GetTeam", teamID).Return(testTeam(), nil)
}

func newTestPlugin(api *plugintest.API) *Plugin {
	p := &Plugin{}
	p.SetAPI(api)
	return p
}

// newTestPluginWithBlocking returns a plugin with the BlockEchoReplies
// settings toggle pinned on/off, instead of the unset (default-on) state
// newTestPlugin leaves.
func newTestPluginWithBlocking(api *plugintest.API, on bool) *Plugin {
	p := newTestPlugin(api)
	p.setConfiguration(&configuration{BlockEchoReplies: &on})
	return p
}

// logMethods are the plugin API's logging methods, in level order.
var logMethods = []string{"LogDebug", "LogInfo", "LogWarn", "LogError"}

// allowLogging lets any plugin log call through the mock at any level..
func allowLogging(api *plugintest.API) {
	for _, method := range logMethods {
		for n := 1; n <= 12; n++ { // log methods are variadic
			args := make([]interface{}, n)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(method, args...).Maybe()
		}
	}
}

// assertLoggedLevel asserts the plugin logged at a given level (`method` must
// be one of logMethods) with a message containing substr. Inspecting recorded
// calls keeps the check arity-independent: level and message are asserted,
// key/value pairs are not.
func assertLoggedLevel(t *testing.T, api *plugintest.API, method, substr string) {
	t.Helper()
	if !slices.Contains(logMethods, method) {
		t.Fatalf("assertLoggedLevel: %q is not a logging method (want one of %v)", method, logMethods)
	}
	for _, call := range api.Calls {
		if call.Method != method {
			continue
		}
		if msg, ok := call.Arguments.Get(0).(string); ok && strings.Contains(msg, substr) {
			return
		}
	}
	t.Errorf("expected a %s call with message containing %q", method, substr)
}

func TestReplyToEchoPostIsRejected(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(newEchoPost(), nil)
	api.On("GetPost", rootID).Return(newOriginalRoot(), nil)
	api.On("GetPostThread", echoID).Return(emptyThread(newEchoPost()), nil)
	expectPermalinkEnv(api)
	var ephemeral *model.Post
	api.On("SendEphemeralPost", replier, mock.Anything).
		Run(func(args mock.Arguments) { ephemeral = args.Get(1).(*model.Post) }).
		Return(nil)
	allowLogging(api)

	out, reason := newTestPlugin(api).MessageWillBePosted(nil, newStrayReply())

	assert.Nil(t, out, "first reply to an empty echo thread must be rejected")
	assert.Contains(t, reason, "belong to the original thread")
	assert.Contains(t, reason, permalinkFor(rootID), "reason carries a permalink to the ORIGINAL thread root")

	// The failed-post UI shows only Retry/Cancel, so the explanation must
	// also arrive in-thread as an ephemeral note (never persisted).
	if assert.NotNil(t, ephemeral, "sender gets an in-thread ephemeral explanation") {
		assert.Equal(t, echoID, ephemeral.RootId, "note lands in the thread they typed into")
		assert.Equal(t, chanID, ephemeral.ChannelId)
		assert.Contains(t, ephemeral.Message, "Your reply was not posted")
		assert.Contains(t, ephemeral.Message, permalinkFor(rootID), "note links to the original thread")
		assert.Contains(t, ephemeral.Message, "/ric")
	}

	api.AssertNotCalled(t, "CreatePost", mock.Anything)
}

// Blocking is unconditional for echo threads (user decision): even when the original thread is
// gone, the stray thread stays sealed. The reason just cannot offer a live link in that case.
func TestReplyToEchoPostRejectedEvenWhenOriginalRootDeleted(t *testing.T) {
	deletedRoot := newOriginalRoot()
	deletedRoot.DeleteAt = 999

	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(newEchoPost(), nil)
	api.On("GetPost", rootID).Return(deletedRoot, nil)
	api.On("GetPostThread", echoID).Return(emptyThread(newEchoPost()), nil)
	expectPermalinkEnv(api)
	api.On("SendEphemeralPost", replier, mock.Anything).Return(nil)
	allowLogging(api)

	out, reason := newTestPlugin(api).MessageWillBePosted(nil, newStrayReply())

	assert.Nil(t, out, "echo threads stay sealed even after the original thread is deleted")
	assert.Contains(t, reason, "belong to the original thread")
	assert.NotContains(t, reason, permalinkFor(rootID), "no dead permalink to a deleted thread")
	assert.Contains(t, reason, "header of this thread's first message")
}

func TestReplyToEchoPostRejectedEvenWhenOriginalRootMissing(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(newEchoPost(), nil)
	api.On("GetPost", rootID).Return(nil, model.NewAppError("GetPost", "store.sql_post.get.app_error", nil, "", 404))
	api.On("GetPostThread", echoID).Return(emptyThread(newEchoPost()), nil)
	expectPermalinkEnv(api)
	api.On("SendEphemeralPost", replier, mock.Anything).Return(nil)
	allowLogging(api)

	// No permalink can be built for a root that cannot be loaded; the reason
	// falls back to pointing at the echo post's header link.
	out, reason := newTestPlugin(api).MessageWillBePosted(nil, newStrayReply())

	assert.Nil(t, out)
	assert.NotContains(t, reason, "/pl/")
	assert.Contains(t, reason, "header of this thread's first message")
}

func TestLegacyEchoThreadWithRepliesAllowed(t *testing.T) {
	// An echo thread that already has replies predates this version (or was
	// typed while the plugin was disabled). Sealing it would interrupt an
	// existing conversation, so further replies flow.
	api := &plugintest.API{}
	echo := newEchoPost()
	api.On("GetPost", echoID).Return(echo, nil)
	api.On("GetPost", rootID).Return(newOriginalRoot(), nil)
	api.On("GetPostThread", echoID).Return(threadWithLegacyReply(echo), nil)
	expectPermalinkEnv(api)
	allowLogging(api)

	reply := newStrayReply()
	out, reason := newTestPlugin(api).MessageWillBePosted(nil, reply)

	assert.Same(t, reply, out)
	assert.Empty(t, reason)
	api.AssertNotCalled(t, "SendEphemeralPost", mock.Anything, mock.Anything)
}

func TestBlockWhenThreadReadFails(t *testing.T) {
	// Fail closed once we KNOW this is an echo thread: a wrongly allowed
	// first reply creates a permanent stray thread, a wrongly blocked one
	// costs the sender one retry.
	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(newEchoPost(), nil)
	api.On("GetPost", rootID).Return(newOriginalRoot(), nil)
	api.On("GetPostThread", echoID).Return(nil, model.NewAppError("GetPostThread", "store.get_thread.app_error", nil, "", 500))
	expectPermalinkEnv(api)
	api.On("SendEphemeralPost", replier, mock.Anything).Return(nil)
	allowLogging(api)

	out, reason := newTestPlugin(api).MessageWillBePosted(nil, newStrayReply())

	assert.Nil(t, out)
	assert.Contains(t, reason, permalinkFor(rootID))
	assertLoggedLevel(t, api, "LogWarn", "failed to load echo thread")
}

func TestAllowedPostsPassThrough(t *testing.T) {
	t.Run("channel-level post is not even inspected", func(t *testing.T) {
		api := &plugintest.API{}
		allowLogging(api)

		post := &model.Post{Id: "chan1post", UserId: replier, ChannelId: chanID, Message: "hello"}
		out, reason := newTestPlugin(api).MessageWillBePosted(nil, post)

		assert.Same(t, post, out)
		assert.Empty(t, reason)
		api.AssertNotCalled(t, "GetPost", mock.Anything)
	})

	t.Run("reply in a regular thread", func(t *testing.T) {
		api := &plugintest.API{}
		api.On("GetPost", rootID).Return(newOriginalRoot(), nil)
		allowLogging(api)

		reply := newStrayReply()
		reply.RootId = rootID
		out, reason := newTestPlugin(api).MessageWillBePosted(nil, reply)

		assert.Same(t, reply, out)
		assert.Empty(t, reason)
	})

	t.Run("echo post's own paired thread reply (rooted at the original root)", func(t *testing.T) {
		// The thread reply /ric creates hangs off the ORIGINAL root, which
		// carries no ric_thread_root_id — the plugin must never trip its own hook.
		api := &plugintest.API{}
		api.On("GetPost", rootID).Return(newOriginalRoot(), nil)
		allowLogging(api)

		paired := &model.Post{Id: "paired1", UserId: echoAuthor, ChannelId: chanID, RootId: rootID, Message: "also sent"}
		out, reason := newTestPlugin(api).MessageWillBePosted(nil, paired)

		assert.Same(t, paired, out)
		assert.Empty(t, reason)
	})
}

func TestReplyToSelfReferencingEchoPostRejected(t *testing.T) {
	// An echo post whose ric_thread_root_id names itself cannot be produced by
	// ExecuteCommand (manual DB edits / imports only). One rule holds: prop
	// present => echo thread => sealed. The reason just cannot offer a link,
	// since the "original thread" would be this very thread.
	selfRef := newEchoPost()
	selfRef.AddProp(ricThreadRootIDProp, echoID) // points at itself

	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(selfRef, nil)
	var ephemeral *model.Post
	api.On("SendEphemeralPost", replier, mock.Anything).
		Run(func(args mock.Arguments) { ephemeral = args.Get(1).(*model.Post) }).
		Return(nil)
	allowLogging(api)

	out, reason := newTestPlugin(api).MessageWillBePosted(nil, newStrayReply())

	assert.Nil(t, out, "self-referencing echo threads are sealed too")
	if assert.NotNil(t, ephemeral) {
		assert.Equal(t, echoID, ephemeral.RootId)
		assert.NotContains(t, ephemeral.Message, "/pl/", "no misleading self-permalink in the note either")
	}
	assert.Contains(t, reason, "created by the /ric command")
	assert.NotContains(t, reason, "/pl/", "no misleading self-permalink")
	assertLoggedLevel(t, api, "LogWarn", "points at itself")
}

func TestHookFailsOpenWhenThreadRootUnreadable(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(nil, model.NewAppError("GetPost", "store.sql_post.get.app_error", nil, "", 500))
	allowLogging(api)

	reply := newStrayReply()
	out, reason := newTestPlugin(api).MessageWillBePosted(nil, reply)

	// Fail open: a reply in a possibly-stray thread beats losing a reply to a
	// transient store error.
	assert.Same(t, reply, out)
	assert.Empty(t, reason)
	assertLoggedLevel(t, api, "LogWarn", "failed to load thread root")
}

func TestRicFromStrayThreadRejected(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(newEchoPost(), nil)
	api.On("GetPost", rootID).Return(newOriginalRoot(), nil)
	expectPermalinkEnv(api)
	allowLogging(api)

	args := &model.CommandArgs{
		Command:   "/ric promoting from the stray thread",
		UserId:    replier,
		ChannelId: chanID,
		RootId:    echoID, // the stray thread the user typed in
	}
	resp, appErr := newTestPlugin(api).ExecuteCommand(nil, args)

	assert.Nil(t, appErr)
	assert.NotNil(t, resp)
	assert.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	assert.Contains(t, resp.Text, "cannot be used in this thread")
	assert.Contains(t, resp.Text, permalinkFor(rootID), "ephemeral error links to the original thread")

	// Nothing is created, mapped, or mutated.
	api.AssertNotCalled(t, "CreatePost", mock.Anything)
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "GetUser", mock.Anything)
}

// The BlockEchoReplies setting is a *bool so a fresh install (no plugin-settings
// entry at all) keeps the guard ENABLED, matching the settings_schema default.
func TestBlockEchoRepliesTriState(t *testing.T) {
	var unset configuration
	assert.True(t, unset.blockEchoReplies(), "unset config keeps the guard on")

	off := false
	disabled := configuration{BlockEchoReplies: &off}
	assert.False(t, disabled.blockEchoReplies())

	on := true
	enabled := configuration{BlockEchoReplies: &on}
	assert.True(t, enabled.blockEchoReplies())
}

func TestBlockingDisabledLetsStrayReplyThrough(t *testing.T) {
	// With the setting off, the hook is inert: the stray reply posts normally
	// and no echo-thread inspection happens at all.
	api := &plugintest.API{}
	allowLogging(api)

	reply := newStrayReply()
	out, reason := newTestPluginWithBlocking(api, false).MessageWillBePosted(nil, reply)

	assert.Same(t, reply, out)
	assert.Empty(t, reason)
	api.AssertNotCalled(t, "GetPost", mock.Anything)
	api.AssertNotCalled(t, "SendEphemeralPost", mock.Anything, mock.Anything)
}

func TestBlockingDisabledAllowsRicInEchoThread(t *testing.T) {
	// The /ric refusal inside echo threads is part of the same guard: with the
	// setting off, the command falls through to the normal flow. The mocked
	// GetUser failure ("Could not find the original author") proves execution
	// passed the guard instead of being refused as an echo thread.
	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(newEchoPost(), nil)
	api.On("GetFileInfos", mock.Anything, mock.Anything, mock.Anything).Return([]*model.FileInfo{}, nil)
	api.On("GetUser", echoAuthor).Return(nil, model.NewAppError("GetUser", "store.sql_user.get.app_error", nil, "", 500))
	allowLogging(api)

	args := &model.CommandArgs{
		Command:   "/ric from an echo thread with blocking off",
		UserId:    replier,
		ChannelId: chanID,
		RootId:    echoID,
	}
	resp, appErr := newTestPluginWithBlocking(api, false).ExecuteCommand(nil, args)

	assert.Nil(t, appErr)
	assert.NotNil(t, resp)
	assert.NotContains(t, resp.Text, "cannot be used in this thread", "echo-thread refusal is part of the disabled guard")
	assert.Contains(t, resp.Text, "Could not find the original author", "command proceeded past the guard")
}

func TestRicFromSelfReferencingEchoThreadRejected(t *testing.T) {
	// Same one rule as replies: prop present => refused. No link is offered
	// (the "original thread" would be this thread itself).
	selfRef := newEchoPost()
	selfRef.AddProp(ricThreadRootIDProp, echoID)

	api := &plugintest.API{}
	api.On("GetPost", echoID).Return(selfRef, nil)
	allowLogging(api)

	args := &model.CommandArgs{
		Command:   "/ric from a self-referencing echo thread",
		UserId:    replier,
		ChannelId: chanID,
		RootId:    echoID,
	}
	resp, appErr := newTestPlugin(api).ExecuteCommand(nil, args)

	assert.Nil(t, appErr)
	assert.NotNil(t, resp)
	assert.Equal(t, model.CommandResponseTypeEphemeral, resp.ResponseType)
	assert.Contains(t, resp.Text, "cannot be used in this thread")
	assertLoggedLevel(t, api, "LogWarn", "points at itself")

	api.AssertNotCalled(t, "CreatePost", mock.Anything)
	api.AssertNotCalled(t, "KVSet", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "GetUser", mock.Anything)
}
