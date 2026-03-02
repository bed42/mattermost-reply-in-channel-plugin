package main

import (
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

// OnDeactivate is invoked when the plugin is deactivated.
func (p *Plugin) OnDeactivate() error {
	p.API.LogInfo("Reply in Channel: Deactivated")
	return nil
}
