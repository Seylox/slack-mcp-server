package edge

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// draftFixture mirrors the shape of a draft as the Slack client receives it.
const draftFixture = `{
  "id": "Dr0TEST1",
  "client_msg_id": "6f0c8a3e-0000-4000-8000-000000000001",
  "blocks": [{"type": "rich_text", "block_id": "abc", "elements": [
    {"type": "rich_text_section", "elements": [{"type": "text", "text": "hello "}, {"type": "emoji", "name": "wave"}]}
  ]}],
  "destinations": [{"channel_id": "C0TEST", "thread_ts": "1700000000.000100", "broadcast": false}],
  "file_ids": [],
  "date_created": 1700000500,
  "date_scheduled": 0,
  "last_updated_ts": "1700000500.0000010",
  "is_deleted": false,
  "is_sent": false
}`

func richTextBlocks() []slack.Block {
	return []slack.Block{slack.NewRichTextBlock("",
		slack.NewRichTextSection(slack.NewRichTextSectionTextElement("hi", nil)))}
}

func TestUnitDraftsCreate(t *testing.T) {
	t.Run("sends the composer's request shape", func(t *testing.T) {
		cl, stub := newTestThreadsClient(`{"ok": true, "draft": ` + draftFixture + `}`)

		draft, err := cl.DraftsCreate(context.Background(), DraftDestination{ChannelID: "C0TEST"}, richTextBlocks())
		require.NoError(t, err)

		assert.Equal(t, "https://example.slack.com/api/drafts.create", stub.lastReq.URL.String())
		form := stub.lastForm
		assert.Equal(t, "xoxc-test-token", form.Get("token"))
		assert.JSONEq(t, `[{"type":"rich_text","elements":[{"type":"rich_text_section","elements":[{"type":"text","text":"hi"}]}]}]`, form.Get("blocks"))
		assert.JSONEq(t, `[{"channel_id":"C0TEST"}]`, form.Get("destinations"))
		assert.Equal(t, "[]", form.Get("file_ids"))
		assert.Equal(t, "true", form.Get("is_from_composer"))
		id, err := uuid.Parse(form.Get("client_msg_id"))
		require.NoError(t, err, "client_msg_id must be a UUID")
		assert.Equal(t, uuid.Version(4), id.Version())

		assert.Equal(t, "Dr0TEST1", draft.ID)
		assert.Equal(t, "1700000500.0000010", draft.LastUpdatedTs)
	})

	t.Run("thread replies carry thread_ts and an explicit broadcast flag", func(t *testing.T) {
		cl, stub := newTestThreadsClient(`{"ok": true, "draft": ` + draftFixture + `}`)
		_, err := cl.DraftsCreate(context.Background(), DraftDestination{ChannelID: "C0TEST", ThreadTs: "1700000000.000100"}, richTextBlocks())
		require.NoError(t, err)
		assert.JSONEq(t, `[{"channel_id":"C0TEST","thread_ts":"1700000000.000100","broadcast":false}]`, stub.lastForm.Get("destinations"))

		broadcast := true
		_, err = cl.DraftsCreate(context.Background(), DraftDestination{ChannelID: "C0TEST", ThreadTs: "1700000000.000100", Broadcast: &broadcast}, richTextBlocks())
		require.NoError(t, err)
		assert.JSONEq(t, `[{"channel_id":"C0TEST","thread_ts":"1700000000.000100","broadcast":true}]`, stub.lastForm.Get("destinations"))
	})

	t.Run("each draft gets its own client_msg_id", func(t *testing.T) {
		cl, stub := newTestThreadsClient(`{"ok": true, "draft": ` + draftFixture + `}`)
		_, err := cl.DraftsCreate(context.Background(), DraftDestination{ChannelID: "C0TEST"}, richTextBlocks())
		require.NoError(t, err)
		first := stub.lastForm.Get("client_msg_id")
		_, err = cl.DraftsCreate(context.Background(), DraftDestination{ChannelID: "C0TEST"}, richTextBlocks())
		require.NoError(t, err)
		assert.NotEqual(t, first, stub.lastForm.Get("client_msg_id"))
	})

	t.Run("Slack errors surface as APIError", func(t *testing.T) {
		cl, _ := newTestThreadsClient(`{"ok": false, "error": "attached_draft_exists"}`)
		_, err := cl.DraftsCreate(context.Background(), DraftDestination{ChannelID: "C0TEST"}, richTextBlocks())
		var apiErr *APIError
		require.True(t, errors.As(err, &apiErr), "got %v", err)
		assert.Equal(t, ErrAttachedDraftExists, apiErr.Err)
		assert.Equal(t, "drafts.create", apiErr.Endpoint)
	})
}

func TestUnitDraftsList(t *testing.T) {
	t.Run("requests active drafts and decodes them", func(t *testing.T) {
		cl, stub := newTestThreadsClient(`{"ok": true, "drafts": [` + draftFixture + `]}`)

		drafts, err := cl.DraftsList(context.Background(), 25)
		require.NoError(t, err)

		assert.Equal(t, "https://example.slack.com/api/drafts.list", stub.lastReq.URL.String())
		assert.Equal(t, "true", stub.lastForm.Get("is_active"))
		assert.Equal(t, "25", stub.lastForm.Get("limit"))

		require.Len(t, drafts, 1)
		d := drafts[0]
		assert.Equal(t, "Dr0TEST1", d.ID)
		require.Len(t, d.Destinations, 1)
		assert.Equal(t, "C0TEST", d.Destinations[0].ChannelID)
		assert.Equal(t, "1700000000.000100", d.Destinations[0].ThreadTs)
		assert.Equal(t, int64(1700000500), d.DateCreated)
		require.Len(t, d.Blocks.BlockSet, 1)
		assert.IsType(t, &slack.RichTextBlock{}, d.Blocks.BlockSet[0])
	})

	t.Run("Slack errors surface as APIError", func(t *testing.T) {
		cl, _ := newTestThreadsClient(`{"ok": false, "error": "not_allowed_token_type"}`)
		_, err := cl.DraftsList(context.Background(), 0)
		var apiErr *APIError
		require.True(t, errors.As(err, &apiErr), "got %v", err)
		assert.Equal(t, "not_allowed_token_type", apiErr.Err)
	})
}
