package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatQuotedReply(t *testing.T) {
	t.Run("short message", func(t *testing.T) {
		result := formatQuotedReply("alice", "Hello world", "My reply", "")
		assert.Equal(t, "Replying to **@alice**:\n> Hello world\n\nMy reply", result)
	})

	t.Run("long message gets truncated", func(t *testing.T) {
		longMsg := strings.Repeat("a", 250)
		result := formatQuotedReply("bob", longMsg, "Reply", "")
		assert.Contains(t, result, "...")
		// The quoted portion should be at most 200 chars + "..."
		lines := strings.SplitN(result, "\n\n", 2)
		// lines[0] is "Replying to **@bob**:\n> aaa..."
		quoteParts := strings.SplitN(lines[0], "\n> ", 2)
		assert.Equal(t, 203, len(quoteParts[1])) // 200 + "..."
	})

	t.Run("multiline message", func(t *testing.T) {
		result := formatQuotedReply("charlie", "Line 1\nLine 2\nLine 3", "My reply", "")
		expected := "Replying to **@charlie**:\n> Line 1\n> Line 2\n> Line 3\n\nMy reply"
		assert.Equal(t, expected, result)
	})

	t.Run("strips existing blockquotes", func(t *testing.T) {
		result := formatQuotedReply("dave", "> Quoted text\nNormal text", "Reply", "")
		// The blockquoted line should be stripped
		assert.NotContains(t, result, "Quoted text")
		assert.Contains(t, result, "Normal text")
	})

	t.Run("empty original after stripping blockquotes", func(t *testing.T) {
		result := formatQuotedReply("eve", "> Only a quote", "Reply", "")
		assert.Equal(t, "Replying to **@eve**:\n> \n\nReply", result)
	})

	t.Run("URLs are wrapped in backticks to prevent previews", func(t *testing.T) {
		result := formatQuotedReply("frank", "Check this out https://example.com/page", "Nice!", "")
		assert.Contains(t, result, "`https://example.com/page`")
		assert.Equal(t, "Replying to **@frank**:\n> Check this out `https://example.com/page`\n\nNice!", result)
	})

	t.Run("message that is only a URL", func(t *testing.T) {
		result := formatQuotedReply("grace", "https://news.ycombinator.com/item?id=12345", "Interesting", "")
		assert.Equal(t, "Replying to **@grace**:\n> `https://news.ycombinator.com/item?id=12345`\n\nInteresting", result)
	})

	t.Run("multiple URLs are all wrapped", func(t *testing.T) {
		result := formatQuotedReply("hank", "See https://a.com and http://b.com too", "Thanks", "")
		assert.Contains(t, result, "`https://a.com`")
		assert.Contains(t, result, "`http://b.com`")
	})

	t.Run("with permalink", func(t *testing.T) {
		result := formatQuotedReply("alice", "Hello world", "My reply", "https://mm.example.com/myteam/pl/abc123")
		assert.Equal(t, "[Replying to **@alice**'s thread](https://mm.example.com/myteam/pl/abc123):\n> Hello world\n\nMy reply", result)
	})
}

func TestUpdateNewerRepliesLink(t *testing.T) {
	t.Run("appends link when none exists", func(t *testing.T) {
		msg := "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello\n\nMy reply"
		result := updateNewerRepliesLink(msg, "https://mm.example.com/t/pl/new123")
		assert.Equal(t, msg+"\n\n[view newer replies](https://mm.example.com/t/pl/new123)", result)
	})

	t.Run("replaces existing link", func(t *testing.T) {
		msg := "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello\n\nMy reply\n\n[view newer replies](https://mm.example.com/t/pl/old456)"
		result := updateNewerRepliesLink(msg, "https://mm.example.com/t/pl/new789")
		expected := "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello\n\nMy reply\n\n[view newer replies](https://mm.example.com/t/pl/new789)"
		assert.Equal(t, expected, result)
	})

	t.Run("does not duplicate link", func(t *testing.T) {
		msg := "Some post\n\n[view newer replies](https://mm.example.com/t/pl/first)"
		result := updateNewerRepliesLink(msg, "https://mm.example.com/t/pl/second")
		assert.Equal(t, 1, strings.Count(result, "view newer replies"))
	})
}

func TestExtractReplyFromChannelPost(t *testing.T) {
	t.Run("extracts reply from channel post", func(t *testing.T) {
		msg := "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello world\n\nMy edited reply"
		assert.Equal(t, "My edited reply", extractReplyFromChannelPost(msg))
	})

	t.Run("extracts reply with view newer replies link", func(t *testing.T) {
		msg := "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello\n\nMy reply\n\n[view newer replies](https://mm.example.com/t/pl/xyz)"
		assert.Equal(t, "My reply", extractReplyFromChannelPost(msg))
	})

	t.Run("returns full message if no match", func(t *testing.T) {
		msg := "just a plain message"
		assert.Equal(t, "just a plain message", extractReplyFromChannelPost(msg))
	})
}

func TestExtractReplyFromThreadPost(t *testing.T) {
	t.Run("extracts reply from thread post", func(t *testing.T) {
		msg := "My reply text\n\n> [Also sent to ~testing](https://mm.example.com/t/pl/abc)"
		assert.Equal(t, "My reply text", extractReplyFromThreadPost(msg))
	})

	t.Run("returns full message if no suffix", func(t *testing.T) {
		msg := "just a plain message"
		assert.Equal(t, "just a plain message", extractReplyFromThreadPost(msg))
	})
}

func TestReplaceReplyInChannelPost(t *testing.T) {
	t.Run("replaces reply preserving header and quote", func(t *testing.T) {
		msg := "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello world\n\nOld reply"
		result := replaceReplyInChannelPost(msg, "New reply")
		assert.Equal(t, "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello world\n\nNew reply", result)
	})

	t.Run("preserves view newer replies link", func(t *testing.T) {
		msg := "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello\n\nOld reply\n\n[view newer replies](https://mm.example.com/t/pl/xyz)"
		result := replaceReplyInChannelPost(msg, "New reply")
		assert.Equal(t, "[Replying to **@alice**'s thread](https://mm.example.com/t/pl/abc):\n> Hello\n\nNew reply\n\n[view newer replies](https://mm.example.com/t/pl/xyz)", result)
	})
}

func TestReplaceReplyInThreadPost(t *testing.T) {
	t.Run("replaces reply preserving suffix", func(t *testing.T) {
		msg := "Old reply\n\n> [Also sent to ~testing](https://mm.example.com/t/pl/abc)"
		result := replaceReplyInThreadPost(msg, "New reply")
		assert.Equal(t, "New reply\n\n> [Also sent to ~testing](https://mm.example.com/t/pl/abc)", result)
	})

	t.Run("replaces entire message if no suffix", func(t *testing.T) {
		msg := "Old reply without suffix"
		result := replaceReplyInThreadPost(msg, "New reply")
		assert.Equal(t, "New reply", result)
	})
}
