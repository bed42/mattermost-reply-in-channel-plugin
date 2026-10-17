package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// ricThreadRootIDProp links a /ric channel echo post to the root of the thread it echoes.
const ricThreadRootIDProp = "ric_thread_root_id"

// Plugin implements the interface expected by the Mattermost server to communicate between the server and plugin processes.
type Plugin struct {
	plugin.MattermostPlugin

	// configurationLock synchronizes access to the configuration.
	configurationLock sync.RWMutex

	// configuration is the active plugin configuration.
	configuration *configuration
}

// OnActivate is invoked when the plugin is activated.
func (p *Plugin) OnActivate() error {
	if err := p.API.RegisterCommand(&model.Command{
		Trigger:          "ric",
		AutoComplete:     true,
		AutoCompleteDesc: "Reply in channel from a thread (usage: /ric your message)",
		AutoCompleteHint: "[message]",
	}); err != nil {
		return fmt.Errorf("failed to register /ric command: %w", err)
	}

	p.API.LogInfo("Reply in Channel: Activated")
	return nil
}

// ExecuteCommand handles the /ric slash command.
func (p *Plugin) ExecuteCommand(c *plugin.Context, args *model.CommandArgs) (*model.CommandResponse, *model.AppError) {
	if args.Command == "" || !strings.HasPrefix(args.Command, "/ric") {
		return nil, nil
	}

	message := strings.TrimSpace(strings.TrimPrefix(args.Command, "/ric"))
	if message == "" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Usage: `/ric your message here`",
		}, nil
	}

	if args.RootId == "" {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "The `/ric` command must be used from within a thread.",
		}, nil
	}

	// Get the root post of the thread
	rootPost, appErr := p.API.GetPost(args.RootId)
	if appErr != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Could not find the thread's root post.",
		}, nil
	}

	// If this command was issued from the stray thread hanging off a /ric echo
	// post, refuse: promoting that stray thread would seal the original thread
	// further away from the user. Send them to the original instead.
	if originalRootID, _ := rootPost.GetProp(ricThreadRootIDProp).(string); originalRootID != "" {
		text := "The `/ric` command cannot be used in this thread — it hangs off a `/ric` channel post."
		if originalRootID != rootPost.Id {
			text += " Run `/ric` in the original thread instead — the link is in the post header above."
			if permalink, ok := p.echoOriginalPermalink(originalRootID, args.UserId); ok && permalink != "" {
				text = fmt.Sprintf("The `/ric` command cannot be used in this thread — it hangs off a `/ric` channel post. Run `/ric` in the [original thread](%s) instead.", permalink)
			}
		} else {
			// Self-referencing prop (manual DB edit/import artifact): flag it.
			p.API.LogWarn("ric: thread root's ric_thread_root_id points at itself (should not happen)", "root_id", rootPost.Id)
		}
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         text,
		}, nil
	}

	// Get the root post author
	rootAuthor, appErr := p.API.GetUser(rootPost.UserId)
	if appErr != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Could not find the original author.",
		}, nil
	}

	// Attempt to recover orphaned file attachments from the slash command.
	// Mattermost uploads files but doesn't pass FileIds to CommandArgs,
	// so we search for recent unattached files by this user in this channel.
	orphanedFileIds := p.findOrphanedFiles(args.UserId, args.ChannelId)

	// Create channel post first without permalink
	channelPost := &model.Post{
		UserId:    args.UserId,
		ChannelId: args.ChannelId,
		Message:   formatQuotedReply(rootAuthor.Username, rootPost.Message, message, ""),
	}
	channelPost.AddProp(ricThreadRootIDProp, args.RootId)

	// Attach recovered files to the channel post (use copies so originals go to thread)
	if len(orphanedFileIds) > 0 {
		copiedFileIds, copyErr := p.API.CopyFileInfos(args.UserId, orphanedFileIds)
		if copyErr != nil {
			p.API.LogWarn("Failed to copy file infos for channel post", "error", copyErr.Error())
		} else {
			channelPost.FileIds = copiedFileIds
			p.API.LogDebug("Attached copied files to channel post", "fileIds", fmt.Sprintf("%v", copiedFileIds))
		}
	}

	createdChannelPost, appErr := p.API.CreatePost(channelPost)
	if appErr != nil {
		return &model.CommandResponse{
			ResponseType: model.CommandResponseTypeEphemeral,
			Text:         "Failed to post reply in channel.",
		}, nil
	}

	// Store mapping from thread root ID → channel post IDs so MessageHasBeenPosted can find them
	p.addRicPostMapping(args.RootId, createdChannelPost.Id)

	// Create the thread reply with a link to the channel post
	channelPostLink := p.buildPermalink(createdChannelPost, args.UserId)
	channel, _ := p.API.GetChannel(args.ChannelId)

	threadMessage := message
	if channelPostLink != "" && channel != nil {
		channelRef := formatChannelRef(channel, channelPostLink)
		threadMessage = fmt.Sprintf("%s\n\n> %s", message, channelRef)
	}

	threadPost := &model.Post{
		UserId:    args.UserId,
		ChannelId: args.ChannelId,
		RootId:    args.RootId,
		Message:   threadMessage,
	}

	// Attach original orphaned files to the thread post
	if len(orphanedFileIds) > 0 {
		threadPost.FileIds = model.StringArray(orphanedFileIds)
		p.API.LogDebug("Attached original files to thread post", "fileIds", fmt.Sprintf("%v", orphanedFileIds))
	}

	createdThreadPost, threadErr := p.API.CreatePost(threadPost)
	if threadErr != nil {
		p.API.LogWarn("Failed to create thread reply", "error", threadErr.Error())
	}

	// Now update the channel post with a permalink to the thread reply
	if createdThreadPost != nil {
		threadPostLink := p.buildPermalink(createdThreadPost, args.UserId)
		if threadPostLink != "" {
			createdChannelPost.Message = formatQuotedReply(rootAuthor.Username, rootPost.Message, message, threadPostLink)
			if _, updateErr := p.API.UpdatePost(createdChannelPost); updateErr != nil {
				p.API.LogWarn("Failed to update channel post with thread link", "error", updateErr.Error())
			}
		}

		// Store bidirectional mapping so edits can be synced between the two posts
		p.storePostPair(createdChannelPost.Id, createdThreadPost.Id)
	}

	return &model.CommandResponse{}, nil
}

// echoOriginalPermalink builds a permalink to the original thread root an echo
// post refers to. It returns ok=false when the original root is deleted or
// cannot be loaded: a link to a dead thread is noise, and the echo post's own
// header carries the same link as a fallback pointer.
func (p *Plugin) echoOriginalPermalink(originalRootID, actingUserID string) (string, bool) {
	originalRoot, appErr := p.API.GetPost(originalRootID)
	if appErr != nil {
		p.API.LogWarn("ric: could not load original thread root", "original_root_id", originalRootID, "error", appErr.Error())
		return "", false
	}
	if originalRoot.DeleteAt != 0 {
		// GetPost returns deleted posts without error; DeleteAt is the only tell.
		return "", false
	}
	return p.buildPermalink(originalRoot, actingUserID), true
}

// MessageWillBePosted rejects replies typed into the stray thread that hangs
// off a /ric echo post: those replies belong to the original thread the echo
// post mirrors. The hook runs BEFORE persistence (v11.7.0 CreatePost), so a
// rejection leaves no half-committed state; the server prefixes the reason
// with "Post rejected by plugin. " and shows it to the sender.
//
// Blocking applies to EMPTY echo threads: the first stray reply is rejected
// with a permalink to the original thread (or the echo post's header link
// when the original is deleted/unloadable). An echo thread that already has
// replies predates this version — it continues and fades out instead of
// being sealed mid-conversation.
func (p *Plugin) MessageWillBePosted(c *plugin.Context, post *model.Post) (*model.Post, string) {
	if post.RootId == "" {
		return post, "" // channel-level post, never a reply to an echo post
	}

	root, appErr := p.API.GetPost(post.RootId)
	if appErr != nil {
		// Fail open: we cannot tell whether this thread root is an echo post.
		p.API.LogWarn("ric block: failed to load thread root, allowing post", "root_id", post.RootId, "error", appErr.Error())
		return post, ""
	}

	originalRootID, _ := root.GetProp(ricThreadRootIDProp).(string)
	if originalRootID == "" {
		return post, "" // regular thread reply
	}
	if originalRootID == root.Id {
		p.API.LogWarn("ric block: thread root's ric_thread_root_id points at itself (should not happen)", "post_id", post.Id, "root_id", post.RootId)
		return p.rejectEchoReply(post, "Replies to this message are not allowed — this thread's root was created by the /ric command", "")
	}

	reason := "Replies to this message belong to the original thread"
	permalink, havePermalink := p.echoOriginalPermalink(originalRootID, post.UserId)
	if havePermalink && permalink != "" {
		reason += ": " + permalink
	} else {
		permalink = ""
		reason += " — the header of this thread's first message links to it"
	}

	// Legacy exemption: an echo thread that ALREADY has replies predates this
	// version (or was typed while the plugin was disabled). Sealing it would
	// interrupt a conversation that exists, so let it continue and fade out.
	// The block therefore only ever stops the FIRST stray reply — new stray
	// threads can never start.
	thread, threadErr := p.API.GetPostThread(post.RootId)
	if threadErr == nil && len(thread.Order) > 1 { // Order lists every post in the thread, root included
		return post, ""
	}
	if threadErr != nil {
		// Fail closed: unlike the GetPost(RootId) miss above (where we cannot
		// tell echo from regular), we already know this is an echo thread —
		// wrongly allowing a first reply creates a permanent stray thread,
		// while wrongly blocking costs the sender one retry.
		p.API.LogWarn("ric block: failed to load echo thread, rejecting post", "root_id", post.RootId, "error", threadErr.Error())
	}
	return p.rejectEchoReply(post, reason, permalink)
}

// rejectEchoReply blocks a stray reply with the given reason AND mirrors the
// explanation into the echo thread as an ephemeral post (visible only to the
// sender, never persisted). The failed-post UI alone shows only Retry/Cancel
// — the reason string is too easy to miss, so the sender also gets a note
// with a direct link right where they were typing.
func (p *Plugin) rejectEchoReply(post *model.Post, reason, permalink string) (*model.Post, string) {
	help := "Your reply was not posted. This thread hangs off a `/ric` channel post — "
	if permalink != "" {
		help += "reply in the [original thread](" + permalink + ") instead, or use `/ric` from there to share replies back here."
	} else {
		help += "use the link in the header of this thread's first message to reach the original thread, and reply there."
	}
	p.API.SendEphemeralPost(post.UserId, &model.Post{
		UserId:    post.UserId,
		ChannelId: post.ChannelId,
		RootId:    post.RootId, // note appears in the very thread they typed into
		Message:   help,
	})
	return nil, reason
}

// MessageHasBeenPosted updates /ric channel posts with a "view newer replies" link
// when new replies are added to a thread that was previously shared via /ric.
func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	// Only care about thread replies
	if post.RootId == "" {
		return
	}

	// Skip posts created by /ric (avoid update loops)
	if post.GetProp(ricThreadRootIDProp) != nil {
		return
	}
	if strings.Contains(post.Message, "Also sent to") {
		return
	}

	// Look up channel posts created by /ric for this thread
	channelPostIDs := p.getRicPostMappings(post.RootId)
	if len(channelPostIDs) == 0 {
		return
	}

	// Build permalink to the newest reply
	permalink := p.buildPermalink(post, post.UserId)
	if permalink == "" {
		return
	}

	// Update each /ric channel post with the "view newer replies" link
	for _, channelPostID := range channelPostIDs {
		channelPost, appErr := p.API.GetPost(channelPostID)
		if appErr != nil {
			p.API.LogWarn("Failed to get /ric channel post", "post_id", channelPostID, "error", appErr.Error())
			continue
		}

		channelPost.Message = updateNewerRepliesLink(channelPost.Message, permalink)
		if _, updateErr := p.API.UpdatePost(channelPost); updateErr != nil {
			p.API.LogWarn("Failed to update /ric channel post", "post_id", channelPostID, "error", updateErr.Error())
		}
	}
}

// MessageHasBeenUpdated syncs edits between paired /ric channel posts and thread posts.
func (p *Plugin) MessageHasBeenUpdated(c *plugin.Context, newPost *model.Post, oldPost *model.Post) {
	// Only care about message text changes
	if newPost.Message == oldPost.Message {
		return
	}

	// Look up the paired post
	pairedPostID := p.getPairedPostID(newPost.Id)
	if pairedPostID == "" {
		return
	}

	pairedPost, appErr := p.API.GetPost(pairedPostID)
	if appErr != nil {
		p.API.LogWarn("Failed to get paired post for edit sync", "post_id", pairedPostID, "error", appErr.Error())
		return
	}

	isChannelPost := newPost.GetProp(ricThreadRootIDProp) != nil

	var newMessage string
	if isChannelPost {
		// User edited the channel post — extract the new reply text and update the thread post
		newReplyText := extractReplyFromChannelPost(newPost.Message)
		newMessage = replaceReplyInThreadPost(pairedPost.Message, newReplyText)
	} else {
		// User edited the thread post — extract the new reply text and update the channel post
		newReplyText := extractReplyFromThreadPost(newPost.Message)
		newMessage = replaceReplyInChannelPost(pairedPost.Message, newReplyText)
	}

	// Skip if the paired post already has the same message (prevents infinite loop)
	if pairedPost.Message == newMessage {
		return
	}

	pairedPost.Message = newMessage
	if _, updateErr := p.API.UpdatePost(pairedPost); updateErr != nil {
		p.API.LogWarn("Failed to sync edit to paired post", "post_id", pairedPostID, "error", updateErr.Error())
	}
}

// ReactionHasBeenAdded mirrors a reaction added to one /ric post onto its paired post,
// attributed to the same user, so the channel post and thread post stay in sync.
func (p *Plugin) ReactionHasBeenAdded(c *plugin.Context, reaction *model.Reaction) {
	pairedPostID := p.getPairedPostID(reaction.PostId)
	if pairedPostID == "" {
		return
	}

	// Skip if the paired post already has this reaction (prevents infinite loop:
	// mirroring an add re-fires this hook for the paired post).
	if p.hasReaction(pairedPostID, reaction.UserId, reaction.EmojiName) {
		return
	}

	mirror := &model.Reaction{
		UserId:    reaction.UserId,
		PostId:    pairedPostID,
		EmojiName: reaction.EmojiName,
		ChannelId: reaction.ChannelId, // both paired posts live in the same channel
	}
	if _, appErr := p.API.AddReaction(mirror); appErr != nil {
		p.API.LogWarn("Failed to sync added reaction to paired post", "post_id", pairedPostID, "error", appErr.Error())
	}
}

// ReactionHasBeenRemoved mirrors a reaction removal from one /ric post onto its paired post.
func (p *Plugin) ReactionHasBeenRemoved(c *plugin.Context, reaction *model.Reaction) {
	pairedPostID := p.getPairedPostID(reaction.PostId)
	if pairedPostID == "" {
		return
	}

	// Skip if the paired post no longer has this reaction (prevents infinite loop).
	if !p.hasReaction(pairedPostID, reaction.UserId, reaction.EmojiName) {
		return
	}

	mirror := &model.Reaction{
		UserId:    reaction.UserId,
		PostId:    pairedPostID,
		EmojiName: reaction.EmojiName,
		ChannelId: reaction.ChannelId,
	}
	if appErr := p.API.RemoveReaction(mirror); appErr != nil {
		p.API.LogWarn("Failed to sync removed reaction from paired post", "post_id", pairedPostID, "error", appErr.Error())
	}
}

// hasReaction reports whether postID already has a reaction with the given user and emoji.
func (p *Plugin) hasReaction(postID, userID, emojiName string) bool {
	reactions, appErr := p.API.GetReactions(postID)
	if appErr != nil {
		p.API.LogWarn("Failed to get reactions for paired post", "post_id", postID, "error", appErr.Error())
		return false
	}
	for _, r := range reactions {
		if r.UserId == userID && r.EmojiName == emojiName {
			return true
		}
	}
	return false
}

// storePostPair stores a bidirectional mapping between a channel post and its thread post.
func (p *Plugin) storePostPair(channelPostID, threadPostID string) {
	if appErr := p.API.KVSet("ric_pair:"+channelPostID, []byte(threadPostID)); appErr != nil {
		p.API.LogWarn("Failed to store post pair mapping", "error", appErr.Error())
	}
	if appErr := p.API.KVSet("ric_pair:"+threadPostID, []byte(channelPostID)); appErr != nil {
		p.API.LogWarn("Failed to store post pair mapping", "error", appErr.Error())
	}
}

// getPairedPostID returns the paired post ID for a given post, or empty string if not found.
func (p *Plugin) getPairedPostID(postID string) string {
	data, appErr := p.API.KVGet("ric_pair:" + postID)
	if appErr != nil || data == nil {
		return ""
	}
	return string(data)
}

// kvKeyForThread returns the KV store key for a thread's /ric channel post mappings.
func kvKeyForThread(rootID string) string {
	return "ric:" + rootID
}

// addRicPostMapping appends a channel post ID to the list for a given thread root ID.
func (p *Plugin) addRicPostMapping(rootID, channelPostID string) {
	existing := p.getRicPostMappings(rootID)
	existing = append(existing, channelPostID)

	data, err := json.Marshal(existing)
	if err != nil {
		p.API.LogWarn("Failed to marshal /ric post mapping", "error", err.Error())
		return
	}

	if appErr := p.API.KVSet(kvKeyForThread(rootID), data); appErr != nil {
		p.API.LogWarn("Failed to save /ric post mapping", "error", appErr.Error())
	}
}

// getRicPostMappings returns the list of channel post IDs for a given thread root ID.
func (p *Plugin) getRicPostMappings(rootID string) []string {
	data, appErr := p.API.KVGet(kvKeyForThread(rootID))
	if appErr != nil || data == nil {
		return nil
	}

	var postIDs []string
	if err := json.Unmarshal(data, &postIDs); err != nil {
		p.API.LogWarn("Failed to unmarshal /ric post mapping", "error", err.Error())
		return nil
	}
	return postIDs
}

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	p.API.LogInfo("Reply in Channel: Deactivated")
	return nil
}
