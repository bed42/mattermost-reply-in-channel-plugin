# Mattermost "Reply in Channel" Plugin

> **Disclaimer:** This project was built with the guidance of a human developer and implemented primarily by [Claude Code](https://claude.com/claude-code) (Anthropic's AI coding assistant). I didn't have time to dev all of this by hand myself, but did have enough to guide Claude to do so! If LLM-assisted development is something you actively avoid, consider this your fair warning.

## What it does

Adds a `/ric` slash command to Mattermost that lets you reply to a thread inline in the channel — similar to Slack's "Also send to channel" feature. Works on all platforms including **web, desktop, iOS, and Android**.

When you run `/ric your message` from within a thread, the plugin:

1. **Posts a formatted reply in the channel** with a quote of the thread's root message and a clickable link back to the thread
2. **Posts your plain reply in the thread** with a link to the channel post

## What it looks like

### In the channel

```
Replying to @erik's thread:
> Kinda cool to see rust sorta taking off in its own niche. It's a pretty cool language

its slowly taking over parts of the linux stack too isn't it?
```

The "Replying to @erik's thread" text is a clickable link that opens the thread.

### In the thread

```
its slowly taking over parts of the linux stack too isn't it?

> Also sent to ~town-square
```

The "Also sent to ~town-square" is a clickable link to the channel post.

## Usage

From within any thread, type:

```
/ric your reply message here
```

- Must be used from within a thread (not the main channel)
- The message cannot be empty
- Supports full Mattermost markdown, emoji, etc. in your reply
- File attachments are forwarded on a best-effort basis (see Limitations below)

## Features

- **Cross-platform**: Works on web, desktop, and mobile (it's a server-side slash command)
- **URL defanging**: URLs in the quoted original message are wrapped in backticks to prevent duplicate link previews
- **Blockquote stripping**: Existing blockquotes in the original message are stripped to avoid nested quotes
- **Long message truncation**: Original messages over 200 characters are truncated in the quote
- **Permalink support**: Both the channel post and thread reply include clickable links to each other
- **Live "view newer replies" link**: When new replies are added to a thread that was shared via `/ric`, the channel post automatically updates with a "view newer replies" link pointing to the latest reply
- **DM/GM support**: Works in direct messages and group messages (picks the user's first team for permalink construction)

## Building

```bash
make dist
```

The plugin bundle will be at `dist/reply-in-channel-*.tar.gz`.

Other useful targets:

- `make test` — run Go tests
- `make server` — compile server only
- `make deploy` — deploy to a Mattermost instance (requires `MM_SERVICESETTINGS_SITEURL` and `MM_ADMIN_TOKEN` env vars)

## Installation

1. Build the plugin or download a release
2. Go to **System Console > Plugins > Plugin Management**
3. Upload the `.tar.gz` file
4. Enable the plugin

## Architecture

This is a server-only plugin (no webapp component). All functionality is implemented via:

- **Slash command** (`/ric`) registered in `OnActivate`
- **`ExecuteCommand`** hook handles the command, creates posts via `p.API.CreatePost()`, and builds permalinks
- **`MessageHasBeenPosted`** hook listens for new thread replies and updates `/ric` channel posts with a "view newer replies" link
- **KV store** maps thread root IDs to `/ric` channel post IDs so the hook can find posts to update
- **`formatQuotedReply`** handles message formatting (quoting, truncation, URL defanging, blockquote stripping)

### How the permalink dance works

Since the channel post needs a link to the thread reply (and vice versa), and neither post ID exists before creation:

1. Create channel post without a permalink
2. Create thread reply with a link to the channel post
3. Update the channel post with a link to the thread reply

## Limitations

- **File attachments**: Mattermost's slash command API does not pass file attachment IDs to plugins. The plugin attempts to recover recently uploaded orphaned files, but this is best-effort and may not work in all cases. If attachments don't appear, try posting the file separately.

## Security

- Acting user is identified by the `Mattermost-User-ID` header (set by the Mattermost server)
- The command only works in threads the user has access to
- Posts are created as the acting user (no BOT tag)
- Rate limiting is handled by Mattermost's built-in post creation limits
