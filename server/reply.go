package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

const maxQuoteLength = 200

// urlPattern matches http and https URLs.
var urlPattern = regexp.MustCompile(`https?://[^\s)>\]]+`)

// formatQuotedReply formats a reply message with a blockquote of the original.
// If permalink is non-empty, the author's name becomes a clickable link to the original message.
func formatQuotedReply(authorUsername, originalMessage, replyText, permalink string) string {
	quoted := stripBlockquotes(originalMessage)

	if len(quoted) > maxQuoteLength {
		quoted = quoted[:maxQuoteLength] + "..."
	}

	// Wrap URLs in backticks so Mattermost won't auto-preview them in the quote
	quoted = defangURLs(quoted)

	// Ensure blockquote works across newlines
	quoted = strings.ReplaceAll(quoted, "\n", "\n> ")

	var header string
	if permalink != "" {
		header = fmt.Sprintf("[Replying to **@%s**'s thread](%s):\n", authorUsername, permalink)
	} else {
		header = fmt.Sprintf("Replying to **@%s**:\n", authorUsername)
	}

	return fmt.Sprintf("%s> %s\n\n%s", header, quoted, replyText)
}

// defangURLs wraps URLs in backticks to prevent Mattermost from generating link previews.
func defangURLs(text string) string {
	return urlPattern.ReplaceAllString(text, "`$0`")
}

// formatChannelRef builds the "Also sent to" text for the thread reply.
// For public/private channels it uses ~channelname (clickable in Mattermost).
// For DMs/GMs it uses the display name since ~name doesn't render for those.
func formatChannelRef(channel *model.Channel, permalink string) string {
	if channel.Type == model.ChannelTypeOpen || channel.Type == model.ChannelTypePrivate {
		return fmt.Sprintf("[Also sent to ~%s](%s)", channel.Name, permalink)
	}
	// DM/GM: use display name or fall back to generic text
	displayName := channel.DisplayName
	if displayName == "" {
		displayName = "this conversation"
	}
	return fmt.Sprintf("[Also sent to %s](%s)", displayName, permalink)
}

// channelPostReplySeparator separates the header+quote from the reply text in a channel post.
// The format is: "header\n> quote\n\nreply text[\n\n[view newer replies](...)]"
var channelPostReplyPattern = regexp.MustCompile(`(?s)^(.*?\n> .*?\n\n)(.*?)(\n\n\[view newer replies\]\([^\)]+\))?$`)

// threadPostSuffixPattern matches the "Also sent to" suffix in a thread post.
var threadPostSuffixPattern = regexp.MustCompile(`\n\n> \[Also sent to [^\]]+\]\([^\)]+\)$`)

// extractReplyFromChannelPost extracts the user's reply text from a formatted channel post.
// Returns the text between the blockquote and the optional "view newer replies" link.
func extractReplyFromChannelPost(message string) string {
	matches := channelPostReplyPattern.FindStringSubmatch(message)
	if matches == nil {
		return message
	}
	return matches[2]
}

// extractReplyFromThreadPost extracts the user's reply text from a thread post.
// Returns everything before the "Also sent to" suffix.
func extractReplyFromThreadPost(message string) string {
	loc := threadPostSuffixPattern.FindStringIndex(message)
	if loc == nil {
		return message
	}
	return message[:loc[0]]
}

// replaceReplyInChannelPost replaces the reply text in a channel post, preserving the
// header, blockquote, and optional "view newer replies" link.
func replaceReplyInChannelPost(message, newReplyText string) string {
	matches := channelPostReplyPattern.FindStringSubmatch(message)
	if matches == nil {
		return message
	}
	// matches[1] = header + quote + \n\n, matches[3] = optional "view newer replies" link
	return matches[1] + newReplyText + matches[3]
}

// replaceReplyInThreadPost replaces the reply text in a thread post, preserving the
// "Also sent to" suffix.
func replaceReplyInThreadPost(message, newReplyText string) string {
	loc := threadPostSuffixPattern.FindStringIndex(message)
	if loc == nil {
		return newReplyText
	}
	return newReplyText + message[loc[0]:]
}

// newerRepliesPattern matches an existing "view newer replies" link so it can be replaced.
var newerRepliesPattern = regexp.MustCompile(`\n\n\[view newer replies\]\([^\)]+\)`)

// updateNewerRepliesLink appends or replaces a "view newer replies" link on a channel post message.
func updateNewerRepliesLink(message, permalink string) string {
	link := fmt.Sprintf("\n\n[view newer replies](%s)", permalink)
	if newerRepliesPattern.MatchString(message) {
		return newerRepliesPattern.ReplaceAllString(message, link)
	}
	return message + link
}

// stripBlockquotes removes existing blockquote lines from a message to avoid nested quotes.
func stripBlockquotes(message string) string {
	lines := strings.Split(message, "\n")
	var result []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, ">") {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
