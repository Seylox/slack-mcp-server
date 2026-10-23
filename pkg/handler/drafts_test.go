package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newDraftsTestHandler() *DraftsHandler {
	logger := zap.NewNop()
	return &DraftsHandler{logger: logger, convHandler: &ConversationsHandler{logger: logger}}
}

func newDraftsRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = "drafts_create"
	req.Params.Arguments = args
	return req
}

func TestUnitDraftsCreateParams(t *testing.T) {
	h := newDraftsTestHandler()
	ctx := context.Background()

	t.Run("text becomes one rich_text block for the channel", func(t *testing.T) {
		t.Setenv("SLACK_MCP_DRAFT_TOOL", "")
		p, err := h.parseDraftsCreateParams(ctx, newDraftsRequest(map[string]any{
			"channel_id": "C0TEST",
			"text":       "*hi* :wave:",
		}))
		require.NoError(t, err)
		assert.Equal(t, edge.DraftDestination{ChannelID: "C0TEST"}, p.dest)
		require.Len(t, p.blocks, 1)
		assert.Equal(t, "*hi* :wave:", text.RichTextToMrkdwn(p.blocks))
	})

	t.Run("thread reply with broadcast", func(t *testing.T) {
		t.Setenv("SLACK_MCP_DRAFT_TOOL", "")
		p, err := h.parseDraftsCreateParams(ctx, newDraftsRequest(map[string]any{
			"channel_id":      "C0TEST",
			"thread_ts":       "1700000000.000100",
			"reply_broadcast": true,
			"text":            "reply",
		}))
		require.NoError(t, err)
		assert.Equal(t, "1700000000.000100", p.dest.ThreadTs)
		require.NotNil(t, p.dest.Broadcast)
		assert.True(t, *p.dest.Broadcast)
	})

	t.Run("raw rich_text blocks as string or array", func(t *testing.T) {
		t.Setenv("SLACK_MCP_DRAFT_TOOL", "")
		const blocksJSON = `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"raw"}]}]}]`
		var asArray []any
		require.NoError(t, json.Unmarshal([]byte(blocksJSON), &asArray))

		for _, blocks := range []any{blocksJSON, asArray} {
			p, err := h.parseDraftsCreateParams(ctx, newDraftsRequest(map[string]any{
				"channel_id": "C0TEST",
				"blocks":     blocks,
				"text":       "ignored when blocks are given",
			}))
			require.NoError(t, err)
			assert.Equal(t, "raw", text.RichTextToMrkdwn(p.blocks))
		}
	})

	t.Run("channel policy is enforced", func(t *testing.T) {
		t.Setenv("SLACK_MCP_DRAFT_TOOL", "C0ALLOWED")
		_, err := h.parseDraftsCreateParams(ctx, newDraftsRequest(map[string]any{"channel_id": "C0OTHER", "text": "x"}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not allowed")

		_, err = h.parseDraftsCreateParams(ctx, newDraftsRequest(map[string]any{"channel_id": "C0ALLOWED", "text": "x"}))
		require.NoError(t, err)

		t.Setenv("SLACK_MCP_DRAFT_TOOL", "!C0BLOCKED")
		_, err = h.parseDraftsCreateParams(ctx, newDraftsRequest(map[string]any{"channel_id": "C0BLOCKED", "text": "x"}))
		require.Error(t, err)
	})

	t.Run("invalid input is rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_DRAFT_TOOL", "")
		cases := map[string]map[string]any{
			"missing channel":               {"text": "x"},
			"missing content":               {"channel_id": "C0TEST"},
			"blank text":                    {"channel_id": "C0TEST", "text": "  \n "},
			"text without content":          {"channel_id": "C0TEST", "text": ">\n>"},
			"bad thread_ts":                 {"channel_id": "C0TEST", "text": "x", "thread_ts": "1700000000"},
			"broadcast without thread":      {"channel_id": "C0TEST", "text": "x", "reply_broadcast": true},
			"blocks are not JSON":           {"channel_id": "C0TEST", "blocks": "not json"},
			"empty blocks":                  {"channel_id": "C0TEST", "blocks": "[]"},
			"blocks that are not rich_text": {"channel_id": "C0TEST", "blocks": `[{"type":"divider"}]`},
		}
		for name, args := range cases {
			_, err := h.parseDraftsCreateParams(ctx, newDraftsRequest(args))
			assert.Error(t, err, name)
		}
	})
}

func TestUnitDraftsCreateError(t *testing.T) {
	dest := edge.DraftDestination{ChannelID: "C0TEST"}

	exists := draftsCreateError(dest, &edge.APIError{Err: edge.ErrAttachedDraftExists, Endpoint: "drafts.create"})
	assert.Contains(t, exists.Error(), "already has a draft")
	var apiErr *edge.APIError
	assert.True(t, errors.As(exists, &apiErr), "the Slack error stays inspectable")

	other := draftsCreateError(dest, errors.New("boom"))
	assert.Equal(t, "failed to create draft: boom", other.Error())
}

func TestUnitDraftCreatedMessage(t *testing.T) {
	assert.Contains(t, draftCreatedMessage(edge.DraftDestination{ChannelID: "C0TEST"}, "Dr1"), "channel C0TEST (draft_id=Dr1)")
	assert.Contains(t, draftCreatedMessage(edge.DraftDestination{ChannelID: "C0TEST", ThreadTs: "1700000000.000100"}, "Dr1"),
		"channel C0TEST in thread 1700000000.000100")
}

func TestUnitBuildDraftRows(t *testing.T) {
	body := []slack.Block{text.MrkdwnToRichText("• one\n• two")}
	drafts := []edge.Draft{
		{ID: "Dr1", Blocks: slack.Blocks{BlockSet: body}, DateCreated: 1700000000,
			Destinations: []edge.DraftDestination{{ChannelID: "C0KNOWN", ThreadTs: "1700000000.000100"}}},
		{ID: "Dr2", IsSent: true, Destinations: []edge.DraftDestination{{ChannelID: "C0KNOWN"}}},
		{ID: "Dr3", IsDeleted: true, Destinations: []edge.DraftDestination{{ChannelID: "C0KNOWN"}}},
		{ID: "Dr4", Destinations: []edge.DraftDestination{{ChannelID: "C0UNKNOWN"}}},
	}
	names := func(id string) string {
		if id == "C0KNOWN" {
			return "#known"
		}
		return id
	}

	rows := buildDraftRows(drafts, names)
	require.Len(t, rows, 2, "sent and deleted drafts are dropped")
	assert.Equal(t, DraftRow{
		DraftID:     "Dr1",
		ChannelID:   "C0KNOWN",
		ChannelName: "#known",
		ThreadTs:    "1700000000.000100",
		DateCreated: "2023-11-14 22:13",
		Text:        "• one\n• two",
	}, rows[0])
	assert.Equal(t, "C0UNKNOWN", rows[1].ChannelName, "unknown channels fall back to the ID")
	assert.Empty(t, rows[1].DateScheduled)
}
