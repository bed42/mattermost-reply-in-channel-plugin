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
- Cannot be used inside the thread of a `/ric` channel post — run it in the original thread instead (its link is in the post header)
- The message cannot be empty
- Supports full Mattermost markdown, emoji, etc. in your reply
- File attachments are forwarded on a best-effort basis (see Limitations below)

## Features

- **Cross-platform**: Works on web, desktop, and mobile (it's a server-side slash command)
- **Reply blocking**: the first reply typed into a `/ric` channel post's thread is rejected before it's saved; the sender gets a direct link to the original thread — in the rejection message *and* as an in-thread note only they can see (the failed-post "Retry/Cancel" UI alone is too easy to miss). `/ric` itself is also refused inside such threads
- **URL defanging**: URLs in the quoted original message are wrapped in backticks to prevent duplicate link previews
- **Blockquote stripping**: Existing blockquotes in the original message are stripped to avoid nested quotes
- **Long message truncation**: Original messages over 200 characters are truncated in the quote
- **Permalink support**: Both the channel post and thread reply include clickable links to each other
- **Live "view newer replies" link**: When new replies are added to a thread that was shared via `/ric`, the channel post automatically updates with a "view newer replies" link pointing to the latest reply
- **Bidirectional edit sync**: Editing either the channel post or the thread post automatically syncs the change to the other — they behave as one post
- **Reaction sync**: Reactions added or removed on one side of a `/ric` pair are mirrored onto the other
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
- **`ExecuteCommand`** hook handles the command, creates posts via `p.API.CreatePost()`, and builds permalinks; refuses `/ric` issued inside a `/ric` post's own thread
- **`MessageWillBePosted`** hook rejects replies typed into a `/ric` channel post's thread before they are persisted, returning a reason with a permalink to the original thread (the echo post is stamped with a `ric_thread_root_id` prop that identifies it)
- **`MessageHasBeenPosted`** hook listens for new thread replies and updates `/ric` channel posts with a "view newer replies" link
- **`MessageHasBeenUpdated`** hook syncs edits bidirectionally between paired channel/thread posts
- **KV store** maps thread root IDs to `/ric` channel post IDs, and stores bidirectional post pair mappings for edit sync
- **`formatQuotedReply`** handles message formatting (quoting, truncation, URL defanging, blockquote stripping)

### How the permalink dance works

Since the channel post needs a link to the thread reply (and vice versa), and neither post ID exists before creation:

1. Create channel post without a permalink
2. Create thread reply with a link to the channel post
3. Update the channel post with a link to the thread reply

## Limitations

- **File attachments**: Mattermost's slash command API does not pass file attachment IDs to plugins. The plugin attempts to recover recently uploaded orphaned files, but this is best-effort and may not work in all cases. If attachments don't appear, try posting the file separately.
- **Blocking applies to empty echo threads only**: the first reply to a `/ric` post's thread is rejected with a link to the original thread. An echo thread that *already* has replies (created before this version or while the plugin was disabled) keeps accepting replies — an existing conversation fades out instead of being sealed mid-flight. `/ric` is refused in any echo thread regardless.

## Security

- Acting user is identified by the `Mattermost-User-ID` header (set by the Mattermost server)
- The command only works in threads the user has access to
- Posts are created as the acting user (no BOT tag)
- Rate limiting is handled by Mattermost's built-in post creation limits

## License

This project is licensed under the MIT License - see the LICENSE file for details.
