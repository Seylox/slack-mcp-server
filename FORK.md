# About this fork

This fork tracks [korotovsky/slack-mcp-server](https://github.com/korotovsky/slack-mcp-server) and adds a few upstream PRs that are still open (as of 2026-10-20). Upstream `master` is merged in regularly. Once a PR is merged upstream, it's no longer a difference.

| Upstream PR | What it adds | Why |
|---|---|---|
| [#347](https://github.com/korotovsky/slack-mcp-server/pull/347) | `conversations_threads` tool | See which threads have unread replies |
| [#367](https://github.com/korotovsky/slack-mcp-server/pull/367) | Session cookies only sent to Slack hosts over HTTPS | Security: the `xoxd` cookie no longer leaks on redirects |
| [#361](https://github.com/korotovsky/slack-mcp-server/pull/361) | Keep block content next to the text fallback | Bot messages are read in full |
| [#321](https://github.com/korotovsky/slack-mcp-server/pull/321) | `oldest` for `conversations_history` and `conversations_replies` | Fetch everything since a timestamp |
| [#335](https://github.com/korotovsky/slack-mcp-server/pull/335) | `ReplyCount` and `LatestReply` columns | Spot new thread activity cheaply |
| [#233](https://github.com/korotovsky/slack-mcp-server/pull/233) | Resolve user mentions to names | Readable message text |
| [#338](https://github.com/korotovsky/slack-mcp-server/pull/338) | Retry and checkpoint the user cache walk | Startup survives transient Slack errors |
| [#344](https://github.com/korotovsky/slack-mcp-server/pull/344) | gofmt-clean tree | Builds aren't stamped `-dirty` |

## Build

There is no npm package for this fork. Build from source:

    go build -o slack-mcp-server ./cmd/slack-mcp-server

`go install` with the fork path doesn't work, because `go.mod` keeps the upstream module path.
