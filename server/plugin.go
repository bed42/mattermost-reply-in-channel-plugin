package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

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
	channelPost.AddProp("ric_thread_root_id", args.RootId)

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
	}

	return &model.CommandResponse{}, nil
}

// MessageHasBeenPosted updates /ric channel posts with a "view newer replies" link
// when new replies are added to a thread that was previously shared via /ric.
func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	// Only care about thread replies
	if post.RootId == "" {
		return
	}

	// Skip posts created by /ric (avoid update loops)
	if post.GetProp("ric_thread_root_id") != nil {
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
