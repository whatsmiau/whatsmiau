package whatsmiau

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

// --- SendGroupStatus ---

// ErrInvalidGroupStatusJID is returned when the group status target is not a group JID.
var ErrInvalidGroupStatusJID = errors.New("groupJid must be a group JID (…@g.us)")

type SendGroupStatusRequest struct {
	InstanceID      string     `json:"instance_id"`
	GroupJID        *types.JID `json:"group_jid"`
	Type            string     `json:"type"` // text | image | audio | video
	Content         string     `json:"content"`
	Caption         string     `json:"caption"`
	BackgroundColor string     `json:"background_color"`
	Font            int        `json:"font"`
}

type SendGroupStatusResponse struct {
	ID        string    `json:"id"`
	GroupJID  types.JID `json:"group_jid"`
	CreatedAt time.Time `json:"created_at"`
}

// groupStatusClient is the part of the WhatsApp client a group status needs;
// *whatsmeow.Client implements it.
type groupStatusClient interface {
	statusMediaUploader
	SendGroupStatus(ctx context.Context, groupJID types.JID, inner *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
}

func validateGroupStatusRequest(data *SendGroupStatusRequest) error {
	if data.GroupJID == nil || data.GroupJID.Server != types.GroupServer || data.GroupJID.User == "" {
		return ErrInvalidGroupStatusJID
	}
	return validateStatusContent(data.Type, data.BackgroundColor)
}

// SendGroupStatus posts a status update to a group (the ring around the group
// picture), visible to every member of the group. The instance's number must be
// a member of the group.
func (s *Whatsmiau) SendGroupStatus(ctx context.Context, data *SendGroupStatusRequest) (*SendGroupStatusResponse, error) {
	if err := validateGroupStatusRequest(data); err != nil {
		return nil, err
	}
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}
	// Checked before the media is downloaded and uploaded, so that an offline instance gets
	// the same refusal for every type and a one-time media URL is not spent.
	if client.Store.GetJID().IsEmpty() {
		return nil, whatsmeow.ErrNotLoggedIn
	}
	if !client.IsConnected() || !client.IsLoggedIn() {
		return nil, ErrInstanceNotConnected
	}
	return s.sendGroupStatus(ctx, client, data)
}

func (s *Whatsmiau) sendGroupStatus(ctx context.Context, client groupStatusClient, data *SendGroupStatusRequest) (*SendGroupStatusResponse, error) {
	msg, err := s.buildStatusMessage(ctx, client, &SendStatusRequest{
		InstanceID:      data.InstanceID,
		Type:            data.Type,
		Content:         data.Content,
		Caption:         data.Caption,
		BackgroundColor: data.BackgroundColor,
		Font:            data.Font,
	})
	if errors.Is(err, ErrInvalidStatusType) {
		return nil, err
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatusContentUnavailable, err)
	}

	res, err := client.SendGroupStatus(ctx, *data.GroupJID, msg)
	if err != nil {
		return nil, err
	}
	return &SendGroupStatusResponse{ID: res.ID, GroupJID: *data.GroupJID, CreatedAt: res.Timestamp}, nil
}
