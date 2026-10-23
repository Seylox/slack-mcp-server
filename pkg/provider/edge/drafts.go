package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/trace"

	"github.com/google/uuid"
	"github.com/slack-go/slack"
)

// drafts.* API — internal Slack APIs behind the composer's drafts and the
// "Drafts & sent" view. A draft is never sent: the user opens it in Slack,
// edits it and sends it. Only accessible with browser session tokens
// (xoxc/xoxd).

// ErrAttachedDraftExists is the error Slack returns when it refuses a draft
// because the destination's composer already holds one. Slack does not always
// refuse: creating a second draft for the same channel has been seen to work.
const ErrAttachedDraftExists = "attached_draft_exists"

// DraftDestination is where a draft will be posted once the user sends it.
type DraftDestination struct {
	ChannelID string `json:"channel_id"`
	ThreadTs  string `json:"thread_ts,omitempty"`
	// Broadcast also posts a thread reply to the channel. Only sent for
	// thread replies, as the Slack client does.
	Broadcast *bool `json:"broadcast,omitempty"`
}

type Draft struct {
	ID            string             `json:"id"`
	ClientMsgID   string             `json:"client_msg_id"`
	Blocks        slack.Blocks       `json:"blocks"`
	Destinations  []DraftDestination `json:"destinations"`
	FileIDs       []string           `json:"file_ids"`
	DateCreated   int64              `json:"date_created"`
	DateScheduled int64              `json:"date_scheduled"`
	LastUpdatedTs string             `json:"last_updated_ts"`
	IsDeleted     bool               `json:"is_deleted"`
	IsSent        bool               `json:"is_sent"`
}

// draftsCreateForm mirrors the Slack client's request. blocks, destinations
// and file_ids are JSON documents inside the form.
type draftsCreateForm struct {
	BaseRequest
	Blocks         string `json:"blocks"`
	Destinations   string `json:"destinations"`
	ClientMsgID    string `json:"client_msg_id"`
	FileIDs        string `json:"file_ids"`
	IsFromComposer bool   `json:"is_from_composer"`
}

type draftsCreateResponse struct {
	baseResponse
	Draft Draft `json:"draft"`
}

// DraftsCreate saves a draft for dest. blocks must be rich_text blocks: drafts
// are composer content, and the composer only holds rich text.
func (cl *Client) DraftsCreate(ctx context.Context, dest DraftDestination, blocks []slack.Block) (Draft, error) {
	ctx, task := trace.NewTask(ctx, "DraftsCreate")
	defer task.End()

	if dest.ThreadTs != "" && dest.Broadcast == nil {
		broadcast := false
		dest.Broadcast = &broadcast
	}
	blocksJSON, err := json.Marshal(slack.Blocks{BlockSet: blocks})
	if err != nil {
		return Draft{}, fmt.Errorf("drafts.create: encoding blocks: %w", err)
	}
	destinationsJSON, err := json.Marshal([]DraftDestination{dest})
	if err != nil {
		return Draft{}, fmt.Errorf("drafts.create: encoding destination: %w", err)
	}

	form := draftsCreateForm{
		BaseRequest:    BaseRequest{Token: cl.token},
		Blocks:         string(blocksJSON),
		Destinations:   string(destinationsJSON),
		ClientMsgID:    uuid.NewString(),
		FileIDs:        "[]",
		IsFromComposer: true,
	}

	resp, err := cl.PostForm(ctx, "drafts.create", values(form, true))
	if err != nil {
		return Draft{}, err
	}
	r := draftsCreateResponse{}
	if err := cl.ParseResponse(&r, resp); err != nil {
		return Draft{}, err
	}
	if err := r.validate("drafts.create"); err != nil {
		return Draft{}, err
	}
	return r.Draft, nil
}

type draftsListForm struct {
	BaseRequest
	IsActive bool `json:"is_active"`
	Limit    int  `json:"limit,omitempty"`
}

type draftsListResponse struct {
	baseResponse
	Drafts []Draft `json:"drafts"`
}

// DraftsList returns the user's active drafts, newest first as Slack orders
// them. Slack may still include sent or deleted drafts; callers filter them.
func (cl *Client) DraftsList(ctx context.Context, limit int) ([]Draft, error) {
	ctx, task := trace.NewTask(ctx, "DraftsList")
	defer task.End()

	form := draftsListForm{
		BaseRequest: BaseRequest{Token: cl.token},
		IsActive:    true,
		Limit:       limit,
	}

	resp, err := cl.PostForm(ctx, "drafts.list", values(form, true))
	if err != nil {
		return nil, err
	}
	r := draftsListResponse{}
	if err := cl.ParseResponse(&r, resp); err != nil {
		return nil, err
	}
	if err := r.validate("drafts.list"); err != nil {
		return nil, err
	}
	return r.Drafts, nil
}
