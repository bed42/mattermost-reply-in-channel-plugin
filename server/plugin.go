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

	// Create channel post first without permalink
	channelPost := &model.Post{
		UserId:    args.UserId,
		ChannelId: args.ChannelId,
		Message:   formatQuotedReply(rootAuthor.Username, rootPost.Message, message, ""),
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
		threadMessage = fmt.Sprintf("%s\n\n> [Also sent to ~%s](%s)", message, channel.Name, channelPostLink)
	}

	threadPost := &model.Post{
		UserId:    args.UserId,
		ChannelId: args.ChannelId,
		RootId:    args.RootId,
		Message:   threadMessage,
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
