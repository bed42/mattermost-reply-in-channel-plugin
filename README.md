# Mattermost "Reply in Channel" Plugin

## Goal

Add a Discord-style "Reply in Channel" option to Mattermost. When a user selects this from a post's "..." dropdown menu, they get a modal to type a reply. The reply is posted to the channel (not in a thread) with a blockquote of the original message, attributed to the acting user (not a bot).

## What it should look like in the channel

```
> **@originaluser**: Original message text here...

User's reply text here
```

## Why this is needed

Mattermost's built-in reply always opens a thread. There's no way to reply inline in the channel with context about what you're replying to (like Discord does). Slack has "Also send to channel" for thread replies, but Mattermost has neither feature. The plugin API can't modify the core reply button or thread composer, but we CAN add a custom action to the post dropdown menu.

## Platform support

- **Web/Desktop**: Full support via `registerPostDropdownMenuAction` + custom React modal
- **Mobile**: NOT supported. The Mattermost mobile app does not render webapp plugin dropdown menu actions. This is a platform limitation. A future enhancement could add a `/reply [permalink]` slash command for mobile users.

## Architecture

### Flow

1. User clicks "..." on any message -> sees "Reply in Channel" option
2. Clicking it opens a custom React modal showing the original message as context + a text area
3. User types their reply and clicks Submit
4. Webapp POSTs to `POST /plugins/{pluginId}/api/v1/reply-in-channel` with `{post_id, message}`
5. Server extracts user ID from `Mattermost-User-ID` header (the acting user, already authenticated)
6. Server fetches original post via `p.API.GetPost(postID)` and original author via `p.API.GetUser(post.UserId)`
7. Server creates a new post via `p.API.CreatePost()` with `UserId` set to the acting user's ID
8. The post appears in the channel as the user's own message (no BOT tag) with a blockquote of the original

### Key technical findings

- **`p.API.CreatePost()` accepts any `UserId`** - no permission check on the user ID field. The post appears as that user authored it, with no BOT tag. Only a `from_plugin: "true"` prop is added as metadata.
- **`Mattermost-User-ID` header** is injected by the Mattermost server on all authenticated requests to plugin HTTP endpoints.
- **`registerPostDropdownMenuAction`** filter callback receives the full `Post` object but the action callback receives no arguments. Use a closure pattern: stash the post from the filter, use it in the action.
- **Interactive dialogs require a `trigger_id`** (only from slash commands or button clicks), so a custom React modal via `registerRootComponent` is the way to go for the "..." menu approach.

## Implementation plan

### Scaffold

Start from the Mattermost plugin starter template or copy the structure from `mattermost-social-previews-plugin`. The plugin needs both server (Go) and webapp (TypeScript/React) components.

Plugin ID: `reply-in-channel`

### Files to create

#### Server (Go)

**`server/plugin.go`**

- Standard plugin struct with `MattermostPlugin` embedding
- `OnActivate` / `OnDeactivate` lifecycle hooks

**`server/api.go`**

- `ServeHTTP` with mux router
- Auth middleware checking `Mattermost-User-ID` header
- `POST /api/v1/reply-in-channel` endpoint:
  - Parse JSON body: `{post_id: string, message: string}`
  - Validate inputs (non-empty post_id and message)
  - Fetch original post: `p.API.GetPost(postID)`
  - Fetch original author: `p.API.GetUser(originalPost.UserId)`
  - Verify acting user has access to the channel
  - Format quoted reply message
  - Create post with `p.API.CreatePost(&model.Post{UserId: actingUserID, ChannelId: originalPost.ChannelId, Message: formattedMessage})`
  - Return 200 on success

**`server/reply.go`**

- `formatQuotedReply(authorUsername, originalMessage, replyText string) string`
  - Truncate original message to ~200 chars if longer
  - Replace newlines in the quoted portion so blockquote renders correctly
  - Format: `> **@username**: original text\n\n reply text`
  - Strip any existing blockquotes from original to avoid nested quotes

#### Webapp (TypeScript/React)

**`webapp/src/index.tsx`**

- In `initialize(registry, store)`:
  - Register root component (the reply modal)
  - Register post dropdown menu action "Reply in Channel"
  - Use closure pattern to capture post from filter callback
  - Action dispatches a custom Redux action or uses a simple event emitter to open the modal with the captured post data

**`webapp/src/components/ReplyModal.tsx`** (NEW)

- React component rendering a modal overlay
- Shows original message as read-only blockquote context (author name + message text, truncated if long)
- Text area for the user's reply
- Submit button -> POST to `/plugins/reply-in-channel/api/v1/reply-in-channel`
- Cancel button -> close modal
- Loading state while submitting
- Error handling (show error message if server returns non-200)
- On success: close modal (the new post will appear in the channel automatically via websocket)

### Quote formatting details

```go
func formatQuotedReply(authorUsername, originalMessage, replyText string) string {
    quoted := originalMessage
    if len(quoted) > 200 {
        quoted = quoted[:200] + "..."
    }
    // Ensure blockquote works across newlines
    quoted = strings.ReplaceAll(quoted, "\n", "\n> ")
    return fmt.Sprintf("> **@%s**: %s\n\n%s", authorUsername, quoted, replyText)
}
```

### Security considerations

- Always validate `Mattermost-User-ID` header is present (auth middleware)
- Verify the acting user is a member of the channel before creating the post (use `p.API.GetChannelMember(channelId, userId)`)
- Sanitize/validate post_id exists via `p.API.GetPost()`
- Don't allow empty reply messages
- Rate limiting is handled by Mattermost's built-in post creation limits

### Build

Same Makefile structure as the social previews plugin:

- `make` - lint + test + dist
- `make server` - compile Go
- `make test` - run all tests
- `make dist` - bundle plugin tar.gz
- `make deploy` - deploy to Mattermost instance

### Testing

- Unit test `formatQuotedReply` with various inputs (short message, long message, multiline, messages with existing blockquotes)
- Unit test the API endpoint with mock plugin API
- Manual test: deploy, click "..." on a message, verify modal opens, submit reply, verify post appears in channel as the user with correct quote formatting
