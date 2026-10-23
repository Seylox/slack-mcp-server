package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gocarina/gocsv"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/provider/edge"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"go.uber.org/zap"
)

const (
	draftsDefaultLimit = 50
	draftsMaxLimit     = 100
)

type DraftRow struct {
	DraftID       string `csv:"DraftID"`
	ChannelID     string `csv:"ChannelID"`
	ChannelName   string `csv:"ChannelName"`
	ThreadTs      string `csv:"ThreadTs"`
	DateCreated   string `csv:"DateCreated"`
	DateScheduled string `csv:"DateScheduled"`
	Text          string `csv:"Text"`
}

type DraftsHandler struct {
	apiProvider *provider.ApiProvider
	logger      *zap.Logger
	convHandler *ConversationsHandler
}

func NewDraftsHandler(apiProvider *provider.ApiProvider, logger *zap.Logger, convHandler *ConversationsHandler) *DraftsHandler {
	return &DraftsHandler{apiProvider: apiProvider, logger: logger, convHandler: convHandler}
}

type draftsCreateParams struct {
	dest   edge.DraftDestination
	blocks []slack.Block
}

// DraftsCreateHandler saves a draft in the user's Slack composer without sending it.
func (h *DraftsHandler) DraftsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsCreateHandler called", zap.Any("params", request.Params))

	if ready, err := h.apiProvider.IsReady(); !ready {
		h.logger.Error("API provider not ready", zap.Error(err))
		return nil, err
	}

	params, err := h.parseDraftsCreateParams(ctx, request)
	if err != nil {
		h.logger.Error("Failed to parse drafts_create params", zap.Error(err))
		return nil, err
	}

	draft, err := h.apiProvider.Slack().DraftsCreate(ctx, params.dest, params.blocks)
	if err != nil {
		h.logger.Error("DraftsCreate failed", zap.Error(err))
		return nil, draftsCreateError(params.dest, err)
	}

	return mcp.NewToolResultText(draftCreatedMessage(params.dest, draft.ID)), nil
}

func draftCreatedMessage(dest edge.DraftDestination, draftID string) string {
	where := "channel " + dest.ChannelID
	if dest.ThreadTs != "" {
		where += " in thread " + dest.ThreadTs
	}
	return fmt.Sprintf("Draft saved for %s (draft_id=%s). It was not sent: it waits in that conversation's composer and in Slack's \"Drafts & sent\" view.", where, draftID)
}

func draftsCreateError(dest edge.DraftDestination, err error) error {
	var apiErr *edge.APIError
	if errors.As(err, &apiErr) && apiErr.Err == edge.ErrAttachedDraftExists {
		return fmt.Errorf("channel %s already has a draft in its composer and Slack refused another; "+
			"send, edit or delete that draft in Slack first (%w)", dest.ChannelID, err)
	}
	return fmt.Errorf("failed to create draft: %w", err)
}

func (h *DraftsHandler) parseDraftsCreateParams(ctx context.Context, request mcp.CallToolRequest) (*draftsCreateParams, error) {
	channel := strings.TrimSpace(request.GetString("channel_id", ""))
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}
	channel, err := h.convHandler.resolveChannelID(ctx, channel)
	if err != nil {
		return nil, err
	}
	if policy := os.Getenv("SLACK_MCP_DRAFT_TOOL"); !isChannelAllowedForConfig(channel, policy) {
		return nil, fmt.Errorf("drafts_create is not allowed for channel %q, applied policy: %s", channel, policy)
	}

	dest := edge.DraftDestination{ChannelID: channel}
	if threadTs := strings.TrimSpace(request.GetString("thread_ts", "")); threadTs != "" {
		if !isSlackTimestamp(threadTs) {
			return nil, fmt.Errorf("thread_ts must be a message timestamp like 1234567890.123456, got %q", threadTs)
		}
		dest.ThreadTs = threadTs
	}
	if request.GetBool("reply_broadcast", false) {
		if dest.ThreadTs == "" {
			return nil, errors.New("reply_broadcast needs thread_ts: only thread replies can also be sent to the channel")
		}
		broadcast := true
		dest.Broadcast = &broadcast
	}

	blocks, err := parseDraftBlocks(request)
	if err != nil {
		return nil, err
	}
	return &draftsCreateParams{dest: dest, blocks: blocks}, nil
}

// parseDraftBlocks returns the draft content from either the raw rich_text
// blocks argument or the mrkdwn text argument.
func parseDraftBlocks(request mcp.CallToolRequest) ([]slack.Block, error) {
	raw, hasBlocks := request.GetArguments()["blocks"]
	if hasBlocks && raw != nil && raw != "" {
		var blocksJSON []byte
		if s, ok := raw.(string); ok {
			blocksJSON = []byte(s)
		} else {
			var err error
			if blocksJSON, err = json.Marshal(raw); err != nil {
				return nil, fmt.Errorf("blocks must be a JSON array of rich_text blocks: %w", err)
			}
		}
		var blocks slack.Blocks
		if err := json.Unmarshal(blocksJSON, &blocks); err != nil {
			return nil, fmt.Errorf("blocks must be a JSON array of rich_text blocks: %w", err)
		}
		if len(blocks.BlockSet) == 0 {
			return nil, errors.New("blocks must contain at least one rich_text block")
		}
		for _, b := range blocks.BlockSet {
			if b.BlockType() != slack.MBTRichText {
				return nil, fmt.Errorf("drafts only hold rich_text blocks, got a %q block; use the text argument for formatted text", b.BlockType())
			}
		}
		return blocks.BlockSet, nil
	}

	msgText := request.GetString("text", "")
	if strings.TrimSpace(msgText) == "" {
		return nil, errors.New("either text or blocks must be provided")
	}
	block := text.MrkdwnToRichText(msgText)
	if len(block.Elements) == 0 {
		return nil, errors.New("text has no content to draft")
	}
	return []slack.Block{block}, nil
}

// DraftsListHandler lists the user's unsent drafts.
func (h *DraftsHandler) DraftsListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	h.logger.Debug("DraftsListHandler called", zap.Any("params", request.Params))

	limit := request.GetInt("limit", draftsDefaultLimit)
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be a positive number, got %d", limit)
	}
	if limit > draftsMaxLimit {
		limit = draftsMaxLimit
	}

	drafts, err := h.apiProvider.Slack().DraftsList(ctx, limit)
	if err != nil {
		h.logger.Error("DraftsList failed", zap.Error(err))
		return nil, fmt.Errorf("failed to list drafts: %w", err)
	}

	channelsMaps := h.apiProvider.ProvideChannelsMaps()
	channelName := func(id string) string {
		if cached, ok := channelsMaps.Channels[id]; ok {
			return cached.Name
		}
		return id
	}
	rows := buildDraftRows(drafts, channelName)
	if len(rows) == 0 {
		return mcp.NewToolResultText("No unsent drafts found."), nil
	}

	csvBytes, err := gocsv.MarshalBytes(&rows)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal drafts: %w", err)
	}
	return mcp.NewToolResultText(string(csvBytes)), nil
}

// buildDraftRows keeps the drafts that are neither sent nor deleted, one row
// per destination.
func buildDraftRows(drafts []edge.Draft, channelName func(string) string) []DraftRow {
	rows := []DraftRow{}
	for _, d := range drafts {
		if d.IsSent || d.IsDeleted {
			continue
		}
		body := text.RichTextToMrkdwn(d.Blocks.BlockSet)
		for _, dest := range d.Destinations {
			rows = append(rows, DraftRow{
				DraftID:       d.ID,
				ChannelID:     dest.ChannelID,
				ChannelName:   channelName(dest.ChannelID),
				ThreadTs:      dest.ThreadTs,
				DateCreated:   formatUnixTs(d.DateCreated),
				DateScheduled: formatUnixTs(d.DateScheduled),
				Text:          body,
			})
		}
	}
	return rows
}
