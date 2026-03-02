package main

import (
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// buildPermalink constructs a Mattermost permalink to the given post.
func (p *Plugin) buildPermalink(post *model.Post, actingUserID string) string {
	config := p.API.GetConfig()
	if config == nil || config.ServiceSettings.SiteURL == nil || *config.ServiceSettings.SiteURL == "" {
		p.API.LogWarn("buildPermalink: SiteURL is not configured")
		return ""
	}
	siteURL := strings.TrimRight(*config.ServiceSettings.SiteURL, "/")

	channel, appErr := p.API.GetChannel(post.ChannelId)
	if appErr != nil {
		p.API.LogWarn("buildPermalink: failed to get channel", "channel_id", post.ChannelId, "error", appErr.Error())
		return ""
	}

	teamName := ""
	if channel.TeamId != "" {
		team, teamErr := p.API.GetTeam(channel.TeamId)
		if teamErr != nil {
			p.API.LogWarn("buildPermalink: failed to get team", "team_id", channel.TeamId, "error", teamErr.Error())
			return ""
		}
		teamName = team.Name
	} else {
		// DMs/GMs have no team — pick the first team the acting user belongs to
		teams, teamErr := p.API.GetTeamsForUser(actingUserID)
		if teamErr != nil || len(teams) == 0 {
			p.API.LogWarn("buildPermalink: could not find a team for user", "user_id", actingUserID)
			return ""
		}
		teamName = teams[0].Name
	}

	permalink := fmt.Sprintf("%s/%s/pl/%s", siteURL, teamName, post.Id)
	p.API.LogDebug("buildPermalink: built permalink", "permalink", permalink)
	return permalink
}
