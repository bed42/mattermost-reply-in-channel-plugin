package main

import (
	"fmt"
	"regexp"
	"strings"
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
