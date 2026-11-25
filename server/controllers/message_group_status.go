package controllers

import (
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"github.com/verbeux-ai/whatsmiau/server/dto"
	"github.com/verbeux-ai/whatsmiau/utils"
	"go.mau.fi/whatsmeow"
	"go.uber.org/zap"
)

// mapGroupStatusError is the error contract of sendGroupStatus: the group refusals, then the
// status contract shared with sendStatus (mapStatusError). A 4xx or a 503 means nothing was sent;
// only the 500 leaves the outcome unknown.
func mapGroupStatusError(err error) (int, string) {
	var iqErr *whatsmeow.IQError
	switch {
	case errors.Is(err, whatsmiau.ErrInvalidGroupStatusJID), errors.Is(err, whatsmeow.ErrGroupStatusNotGroup):
		return http.StatusBadRequest, "invalid groupJid"
	case errors.Is(err, whatsmeow.ErrGroupStatusUnsupportedContent):
		return http.StatusBadRequest, "invalid status type"
	case errors.Is(err, whatsmeow.ErrGroupNotFound):
		return http.StatusNotFound, "group not found"
	case errors.Is(err, whatsmeow.ErrNotInGroup):
		return http.StatusForbidden, "not a member of this group"
	case errors.Is(err, whatsmeow.ErrGroupStatusAdminsOnly):
		return http.StatusForbidden, "only admins can send messages in this group"
	case errors.As(err, &iqErr) && iqErr.Code == http.StatusUnauthorized:
		return http.StatusForbidden, "not allowed to post in this group"
	default:
		return mapStatusError(err, "failed to send group status")
	}
}

// SendGroupStatus godoc
// @Summary      Send a group status
// @Description  Posts a status (text/image/audio/video) to a group: it shows up in the ring around the group picture and every member can see it, without saving the number. The instance's number must be a member of the group.
// @Description
// @Description  Errors: 400 invalid body, groupJid or type; 404 instance not found or not logged in, or group not found; 403 the number is not a member of the group or may not post in it (when only admins can send messages, WhatsApp accepts a status from another member but delivers it to nobody, so it is refused here); 409 the instance is not connected; 422 the media could not be loaded (the URL must answer 2xx with an image, video or audio of the declared type) or WhatsApp rejected the status; 429 rate limited; 503 WhatsApp failed or did not answer a lookup made before sending. Nothing was posted on any of these. 500 the outcome is unknown (do not retry blindly).
// @Tags         Message
// @Accept       json
// @Produce      json
// @Security     ApiKeyAuth
// @Param        instance  path      string                     true  "Instance ID"
// @Param        body      body      dto.SendGroupStatusRequest  true  "Group status parameters"
// @Success      200       {object}  dto.SendGroupStatusResponse
// @Failure      400       {object}  utils.HTTPErrorResponse
// @Failure      403       {object}  utils.HTTPErrorResponse
// @Failure      404       {object}  utils.HTTPErrorResponse
// @Failure      409       {object}  utils.HTTPErrorResponse
// @Failure      422       {object}  utils.HTTPErrorResponse
// @Failure      429       {object}  utils.HTTPErrorResponse
// @Failure      500       {object}  utils.HTTPErrorResponse
// @Failure      503       {object}  utils.HTTPErrorResponse
// @Router       /v1/instance/{instance}/message/groupStatus [post]
// @Router       /v1/message/sendGroupStatus/{instance} [post]
func (s *Message) SendGroupStatus(ctx echo.Context) error {
	var request dto.SendGroupStatusRequest
	if err := ctx.Bind(&request); err != nil {
		// Unlike the other routes, a body that does not decode is a 400 here,
		// so that every refusal before sending is a 4xx the caller can trust.
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "failed to bind request body")
	}

	if err := validator.New().Struct(&request); err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid request body")
	}

	groupJID, err := parseGroupJID(request.GroupJid)
	if err != nil {
		return utils.HTTPFail(ctx, http.StatusBadRequest, err, "invalid groupJid")
	}

	res, err := s.whatsmiau.SendGroupStatus(ctx.Request().Context(), &whatsmiau.SendGroupStatusRequest{
		InstanceID:      request.InstanceID,
		GroupJID:        groupJID,
		Type:            request.Type,
		Content:         request.Content,
		Caption:         request.Caption,
		BackgroundColor: request.BackgroundColor,
		Font:            request.Font,
	})
	if err != nil {
		code, message := mapGroupStatusError(err)
		if code >= http.StatusInternalServerError {
			zap.L().Error("Whatsmiau.SendGroupStatus failed", zap.Error(err))
		} else {
			zap.L().Warn("Whatsmiau.SendGroupStatus refused", zap.Int("status", code), zap.Error(err))
		}
		return utils.HTTPFail(ctx, code, err, message)
	}

	return ctx.JSON(http.StatusOK, groupStatusResponse(request.InstanceID, res))
}

// groupStatusResponse answers in the Evolution format of sendStatus. The key
// (group JID + message ID, fromMe) is what deleteMessageForEveryone takes to
// remove the status.
func groupStatusResponse(instanceID string, res *whatsmiau.SendGroupStatusResponse) dto.SendGroupStatusResponse {
	return dto.SendGroupStatusResponse{
		Key: dto.MessageResponseKey{
			RemoteJid: res.GroupJID.String(),
			FromMe:    true,
			Id:        res.ID,
		},
		Status:           "sent",
		MessageType:      "groupStatusMessageV2",
		MessageTimestamp: int(res.CreatedAt.Unix()),
		InstanceId:       instanceID,
	}
}
