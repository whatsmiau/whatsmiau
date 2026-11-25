package whatsmiau

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/verbeux-ai/whatsmiau/services"
	"net/http"
	"regexp"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// --- SendVideo ---

type SendVideoRequest struct {
	InstanceID       string     `json:"instance_id"`
	MediaURL         string     `json:"media_url"`
	Caption          string     `json:"caption"`
	RemoteJID        *types.JID `json:"remote_jid"`
	Mimetype         string     `json:"mimetype"`
	GifPlayback      bool       `json:"gif_playback"`
	Quote            *Quote     `json:"quote,omitempty"`
	MentionsEveryOne bool       `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string   `json:"mentioned,omitempty"`
}

type SendVideoResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// ServerID is only set for channel (newsletter) messages.
	ServerID types.MessageServerID `json:"server_id,omitempty"`
}

func (s *Whatsmiau) SendVideo(ctx context.Context, data *SendVideoRequest) (*SendVideoResponse, error) {
	client, resolved, err := s.loadClientWithJID(ctx, data.InstanceID, data.RemoteJID)
	if err != nil {
		return nil, err
	}
	data.RemoteJID = &resolved
	data.Quote = quoteForChat(resolved, data.Quote)

	mentioned, everyone, err := s.resolveMentions(resolved, data.MentionsEveryOne, data.Mentioned)
	if err != nil {
		return nil, err
	}

	dataBytes, err := s.fetchBytes(ctx, data.MediaURL)
	if err != nil {
		return nil, err
	}

	uploaded, err := uploadMedia(ctx, client, resolved, dataBytes, whatsmeow.MediaVideo)
	if err != nil {
		return nil, err
	}

	if data.Mimetype == "" {
		data.Mimetype = "video/mp4"
	}

	video := waE2E.VideoMessage{
		URL:           proto.String(uploaded.URL),
		Mimetype:      proto.String(data.Mimetype),
		Caption:       proto.String(data.Caption),
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uploaded.FileLength),
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		DirectPath:    proto.String(uploaded.DirectPath),
		ContextInfo:   mediaContextInfo(resolved, buildContextInfo(data.Quote, mentioned, everyone)),
	}
	if data.GifPlayback {
		video.GifPlayback = proto.Bool(true)
	}

	res, err := client.SendMessage(ctx, resolved, &waE2E.Message{VideoMessage: &video}, mediaSendExtra(resolved, uploaded)...)
	if err != nil {
		return nil, err
	}

	return &SendVideoResponse{ID: res.ID, CreatedAt: res.Timestamp, ServerID: res.ServerID}, nil
}

// --- SendPtv (Round/Note Video) ---

type SendPtvRequest struct {
	InstanceID       string     `json:"instance_id"`
	VideoURL         string     `json:"video_url"`
	RemoteJID        *types.JID `json:"remote_jid"`
	Quote            *Quote     `json:"quote,omitempty"`
	MentionsEveryOne bool       `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string   `json:"mentioned,omitempty"`
}

type SendPtvResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// ServerID is only set for channel (newsletter) messages.
	ServerID types.MessageServerID `json:"server_id,omitempty"`
}

func (s *Whatsmiau) SendPtv(ctx context.Context, data *SendPtvRequest) (*SendPtvResponse, error) {
	client, resolved, err := s.loadClientWithJID(ctx, data.InstanceID, data.RemoteJID)
	if err != nil {
		return nil, err
	}
	data.RemoteJID = &resolved
	// whatsmeow has no channel post type for round videos.
	if err := rejectNewsletter(resolved, "round video"); err != nil {
		return nil, err
	}

	mentioned, everyone, err := s.resolveMentions(resolved, data.MentionsEveryOne, data.Mentioned)
	if err != nil {
		return nil, err
	}

	dataBytes, err := s.fetchBytes(ctx, data.VideoURL)
	if err != nil {
		return nil, err
	}

	uploaded, err := uploadMedia(ctx, client, resolved, dataBytes, whatsmeow.MediaVideo)
	if err != nil {
		return nil, err
	}

	video := waE2E.VideoMessage{
		URL:             proto.String(uploaded.URL),
		Mimetype:        proto.String("video/mp4"),
		FileSHA256:      uploaded.FileSHA256,
		FileLength:      proto.Uint64(uploaded.FileLength),
		MediaKey:        uploaded.MediaKey,
		FileEncSHA256:   uploaded.FileEncSHA256,
		DirectPath:      proto.String(uploaded.DirectPath),
		VideoSourceType: waE2E.VideoMessage_USER_VIDEO.Enum(),
		ContextInfo:     buildContextInfo(data.Quote, mentioned, everyone),
	}

	res, err := client.SendMessage(ctx, resolved, &waE2E.Message{PtvMessage: &video}, mediaSendExtra(resolved, uploaded)...)
	if err != nil {
		return nil, err
	}

	return &SendPtvResponse{ID: res.ID, CreatedAt: res.Timestamp, ServerID: res.ServerID}, nil
}

// --- SendSticker ---

type SendStickerRequest struct {
	InstanceID       string     `json:"instance_id"`
	StickerURL       string     `json:"sticker_url"`
	RemoteJID        *types.JID `json:"remote_jid"`
	Quote            *Quote     `json:"quote,omitempty"`
	MentionsEveryOne bool       `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string   `json:"mentioned,omitempty"`
}

type SendStickerResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// ServerID is only set for channel (newsletter) messages.
	ServerID types.MessageServerID `json:"server_id,omitempty"`
}

func (s *Whatsmiau) SendSticker(ctx context.Context, data *SendStickerRequest) (*SendStickerResponse, error) {
	client, resolved, err := s.loadClientWithJID(ctx, data.InstanceID, data.RemoteJID)
	if err != nil {
		return nil, err
	}
	data.RemoteJID = &resolved
	data.Quote = quoteForChat(resolved, data.Quote)

	mentioned, everyone, err := s.resolveMentions(resolved, data.MentionsEveryOne, data.Mentioned)
	if err != nil {
		return nil, err
	}

	dataBytes, err := s.fetchBytes(ctx, data.StickerURL)
	if err != nil {
		return nil, err
	}

	uploaded, err := uploadMedia(ctx, client, resolved, dataBytes, whatsmeow.MediaImage)
	if err != nil {
		return nil, err
	}

	sticker := waE2E.StickerMessage{
		URL:           proto.String(uploaded.URL),
		Mimetype:      proto.String("image/webp"),
		FileSHA256:    uploaded.FileSHA256,
		FileLength:    proto.Uint64(uploaded.FileLength),
		MediaKey:      uploaded.MediaKey,
		FileEncSHA256: uploaded.FileEncSHA256,
		DirectPath:    proto.String(uploaded.DirectPath),
		ContextInfo:   mediaContextInfo(resolved, buildContextInfo(data.Quote, mentioned, everyone)),
	}

	res, err := client.SendMessage(ctx, resolved, &waE2E.Message{StickerMessage: &sticker}, mediaSendExtra(resolved, uploaded)...)
	if err != nil {
		return nil, err
	}

	return &SendStickerResponse{ID: res.ID, CreatedAt: res.Timestamp, ServerID: res.ServerID}, nil
}

// --- SendLocation ---

type SendLocationRequest struct {
	InstanceID       string     `json:"instance_id"`
	RemoteJID        *types.JID `json:"remote_jid"`
	Latitude         float64    `json:"latitude"`
	Longitude        float64    `json:"longitude"`
	Name             string     `json:"name"`
	Address          string     `json:"address"`
	Quote            *Quote     `json:"quote,omitempty"`
	MentionsEveryOne bool       `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string   `json:"mentioned,omitempty"`
}

type SendLocationResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Whatsmiau) SendLocation(ctx context.Context, data *SendLocationRequest) (*SendLocationResponse, error) {
	client, resolved, err := s.loadClientWithJID(ctx, data.InstanceID, data.RemoteJID)
	if err != nil {
		return nil, err
	}
	data.RemoteJID = &resolved
	// Channels cannot post locations.
	if err := rejectNewsletter(resolved, "location message"); err != nil {
		return nil, err
	}

	mentioned, everyone, err := s.resolveMentions(resolved, data.MentionsEveryOne, data.Mentioned)
	if err != nil {
		return nil, err
	}

	loc := &waE2E.LocationMessage{
		DegreesLatitude:  proto.Float64(data.Latitude),
		DegreesLongitude: proto.Float64(data.Longitude),
		ContextInfo:      buildContextInfo(data.Quote, mentioned, everyone),
	}
	if data.Name != "" {
		loc.Name = proto.String(data.Name)
	}
	if data.Address != "" {
		loc.Address = proto.String(data.Address)
	}

	res, err := client.SendMessage(ctx, resolved, &waE2E.Message{LocationMessage: loc})
	if err != nil {
		return nil, err
	}

	return &SendLocationResponse{ID: res.ID, CreatedAt: res.Timestamp}, nil
}

// --- SendContact ---

type SendContactItem struct {
	FullName     string `json:"full_name"`
	Wuid         string `json:"wuid"`
	PhoneNumber  string `json:"phone_number"`
	Organization string `json:"organization"`
	Email        string `json:"email"`
	URL          string `json:"url"`
}

type SendContactRequest struct {
	InstanceID       string            `json:"instance_id"`
	RemoteJID        *types.JID        `json:"remote_jid"`
	Contacts         []SendContactItem `json:"contacts"`
	Quote            *Quote            `json:"quote,omitempty"`
	MentionsEveryOne bool              `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string          `json:"mentioned,omitempty"`
}

type SendContactResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func contactToProto(c SendContactItem) *waE2E.ContactMessage {
	return &waE2E.ContactMessage{
		DisplayName: proto.String(c.FullName),
		Vcard:       proto.String(buildVCard(c.FullName, c.Wuid, c.PhoneNumber, c.Organization, c.Email, c.URL)),
	}
}

func (s *Whatsmiau) SendContact(ctx context.Context, data *SendContactRequest) (*SendContactResponse, error) {
	if len(data.Contacts) == 0 {
		return nil, fmt.Errorf("contacts is required")
	}

	client, resolved, err := s.loadClientWithJID(ctx, data.InstanceID, data.RemoteJID)
	if err != nil {
		return nil, err
	}
	data.RemoteJID = &resolved
	// Channels cannot post contact cards.
	if err := rejectNewsletter(resolved, "contact message"); err != nil {
		return nil, err
	}

	mentioned, everyone, err := s.resolveMentions(resolved, data.MentionsEveryOne, data.Mentioned)
	if err != nil {
		return nil, err
	}

	var msg *waE2E.Message
	if len(data.Contacts) == 1 {
		contact := contactToProto(data.Contacts[0])
		contact.ContextInfo = buildContextInfo(data.Quote, mentioned, everyone)
		msg = &waE2E.Message{ContactMessage: contact}
	} else {
		contacts := make([]*waE2E.ContactMessage, 0, len(data.Contacts))
		for _, c := range data.Contacts {
			contacts = append(contacts, contactToProto(c))
		}
		msg = &waE2E.Message{
			ContactsArrayMessage: &waE2E.ContactsArrayMessage{
				DisplayName: proto.String(data.Contacts[0].FullName),
				Contacts:    contacts,
				ContextInfo: buildContextInfo(data.Quote, mentioned, everyone),
			},
		}
	}

	res, err := client.SendMessage(ctx, resolved, msg)
	if err != nil {
		return nil, err
	}

	return &SendContactResponse{ID: res.ID, CreatedAt: res.Timestamp}, nil
}

// --- SendPoll ---

type SendPollRequest struct {
	InstanceID      string     `json:"instance_id"`
	RemoteJID       *types.JID `json:"remote_jid"`
	Name            string     `json:"name"`
	SelectableCount int        `json:"selectable_count"`
	Values          []string   `json:"values"`
	// CorrectAnswer turns a channel poll into a quiz; it must be one of Values.
	CorrectAnswer    string   `json:"correct_answer,omitempty"`
	Quote            *Quote   `json:"quote,omitempty"`
	MentionsEveryOne bool     `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string `json:"mentioned,omitempty"`
}

type SendPollResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// ServerID is only set for channel (newsletter) messages.
	ServerID types.MessageServerID `json:"server_id,omitempty"`
}

func (s *Whatsmiau) SendPoll(ctx context.Context, data *SendPollRequest) (*SendPollResponse, error) {
	if len(data.Values) < 2 {
		return nil, fmt.Errorf("values requires at least 2 options")
	}

	client, resolved, err := s.loadClientWithJID(ctx, data.InstanceID, data.RemoteJID)
	if err != nil {
		return nil, err
	}
	data.RemoteJID = &resolved

	if isNewsletter(resolved) {
		pollMsg, err := buildNewsletterPoll(data.Name, data.Values, data.SelectableCount, data.CorrectAnswer)
		if err != nil {
			return nil, err
		}
		res, err := client.SendMessage(ctx, resolved, pollMsg)
		if err != nil {
			return nil, err
		}
		// Vote updates only carry the server ID and option hashes.
		rememberNewsletterPost(ctx, services.Redis(), resolved, res.ServerID, res.ID, pollMsg)
		return &SendPollResponse{ID: res.ID, CreatedAt: res.Timestamp, ServerID: res.ServerID}, nil
	}
	if data.CorrectAnswer != "" {
		return nil, requireNewsletter(resolved, "quiz")
	}

	mentioned, everyone, err := s.resolveMentions(resolved, data.MentionsEveryOne, data.Mentioned)
	if err != nil {
		return nil, err
	}

	pollMsg := client.BuildPollCreation(data.Name, data.Values, data.SelectableCount)
	pollMsg.PollCreationMessage.ContextInfo = buildContextInfo(data.Quote, mentioned, everyone)

	res, err := client.SendMessage(ctx, resolved, pollMsg)
	if err != nil {
		return nil, err
	}

	return &SendPollResponse{ID: res.ID, CreatedAt: res.Timestamp, ServerID: res.ServerID}, nil
}

// --- SendStatus ---

type SendStatusRequest struct {
	InstanceID      string   `json:"instance_id"`
	Type            string   `json:"type"` // text | image | audio | video
	Content         string   `json:"content"`
	Caption         string   `json:"caption"`
	BackgroundColor string   `json:"background_color"`
	Font            int      `json:"font"`
	StatusJidList   []string `json:"status_jid_list"`
	AllContacts     bool     `json:"all_contacts"`
}

type SendStatusResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

func statusFontFromInt(font int) waE2E.ExtendedTextMessage_FontType {
	switch font {
	case 1:
		return waE2E.ExtendedTextMessage_SYSTEM_TEXT
	case 2:
		return waE2E.ExtendedTextMessage_FB_SCRIPT
	case 3:
		return waE2E.ExtendedTextMessage_SYSTEM_BOLD
	case 4:
		return waE2E.ExtendedTextMessage_MORNINGBREEZE_REGULAR
	case 5:
		return waE2E.ExtendedTextMessage_CALISTOGA_REGULAR
	default:
		return waE2E.ExtendedTextMessage_SYSTEM
	}
}

var (
	// ErrInvalidStatusType is returned when the status type is not text, image, video or audio.
	ErrInvalidStatusType = errors.New("invalid status type")
	// ErrInvalidStatusBackground is returned when a text status color is not #RRGGBB or #AARRGGBB.
	ErrInvalidStatusBackground = errors.New("backgroundColor must be #RRGGBB or #AARRGGBB")
	// ErrStatusContentUnavailable wraps failures to download, check, convert or upload the
	// status media. Nothing was sent to WhatsApp when it is returned.
	ErrStatusContentUnavailable = errors.New("failed to prepare the status content")
)

var statusBackgroundPattern = regexp.MustCompile(`^#?(?:[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// validateStatusContent checks the type of a status update and, for a text status, its
// background color.
func validateStatusContent(kind, backgroundColor string) error {
	switch strings.ToLower(kind) {
	case "text":
		if color := strings.TrimSpace(backgroundColor); color != "" && !statusBackgroundPattern.MatchString(color) {
			return ErrInvalidStatusBackground
		}
	case "image", "video", "audio":
	default:
		return fmt.Errorf("%w: %s", ErrInvalidStatusType, kind)
	}
	return nil
}

// statusMediaUploader is the part of the WhatsApp client the status builders
// need; *whatsmeow.Client implements it.
type statusMediaUploader interface {
	Upload(ctx context.Context, plaintext []byte, appInfo whatsmeow.MediaType) (whatsmeow.UploadResponse, error)
}

// ErrStatusMediaType is returned when the media of a status is not of the status type, for
// instance an error page or JSON that the media URL answered with 200.
var ErrStatusMediaType = errors.New("status media does not match the status type")

// Size caps on a status media download. They bound the memory one request can take and sit well
// above what a status needs.
const (
	statusImageMaxBytes int64 = 16 << 20
	statusVideoMaxBytes int64 = 64 << 20
	statusAudioMaxBytes int64 = 16 << 20
)

// fetchStatusMedia downloads the media of an image, video or audio status (an http(s) URL or a
// data: URI) and checks that the bytes are of that kind, so that whatever answered the URL (an
// error page, JSON, an empty body) is never posted as a status.
func (s *Whatsmiau) fetchStatusMedia(ctx context.Context, content, kind string) ([]byte, error) {
	limit := statusImageMaxBytes
	switch kind {
	case "video":
		limit = statusVideoMaxBytes
	case "audio":
		limit = statusAudioMaxBytes
	}
	data, err := s.fetchBytesLimit(ctx, content, limit)
	if err != nil {
		return nil, err
	}
	if !statusMediaMatches(kind, data) {
		return nil, fmt.Errorf("%w: %s status got %s", ErrStatusMediaType, kind, http.DetectContentType(data))
	}
	return data, nil
}

// statusMediaMatches sniffs the first bytes of a status media.
func statusMediaMatches(kind string, data []byte) bool {
	if len(data) == 0 {
		return false
	}
	detected := http.DetectContentType(data)
	switch kind {
	case "image":
		return strings.HasPrefix(detected, "image/")
	case "video":
		// The sniffer only names MP4 files that carry an mp4 brand; any ISO base media file
		// (an ftyp box first, as in MP4, MOV and 3GP) is accepted too.
		return strings.HasPrefix(detected, "video/") || len(data) >= 8 && bytes.Equal(data[4:8], []byte("ftyp"))
	case "audio":
		// ffmpeg decodes the audio afterwards and fails on anything else; this only stops text,
		// such as an error page, before it gets there.
		return !strings.HasPrefix(detected, "text/")
	default:
		return false
	}
}

func buildTextStatus(data *SendStatusRequest) *waE2E.Message {
	return &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text:           proto.String(data.Content),
			BackgroundArgb: proto.Uint32(parseStatusBackgroundARGB(data.BackgroundColor)),
			Font:           statusFontFromInt(data.Font).Enum(),
		},
	}
}

func (s *Whatsmiau) buildImageStatus(ctx context.Context, client statusMediaUploader, data *SendStatusRequest) (*waE2E.Message, error) {
	bytesData, err := s.fetchStatusMedia(ctx, data.Content, "image")
	if err != nil {
		return nil, err
	}
	uploaded, err := client.Upload(ctx, bytesData, whatsmeow.MediaImage)
	if err != nil {
		return nil, err
	}
	mimetype, _ := extractMimetype(bytesData, uploaded.URL)
	if mimetype == "" {
		mimetype = "image/jpeg"
	}
	return &waE2E.Message{
		ImageMessage: &waE2E.ImageMessage{
			URL:           proto.String(uploaded.URL),
			Mimetype:      proto.String(mimetype),
			Caption:       proto.String(data.Caption),
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uploaded.FileLength),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			DirectPath:    proto.String(uploaded.DirectPath),
		},
	}, nil
}

func (s *Whatsmiau) buildVideoStatus(ctx context.Context, client statusMediaUploader, data *SendStatusRequest) (*waE2E.Message, error) {
	bytesData, err := s.fetchStatusMedia(ctx, data.Content, "video")
	if err != nil {
		return nil, err
	}
	uploaded, err := client.Upload(ctx, bytesData, whatsmeow.MediaVideo)
	if err != nil {
		return nil, err
	}
	return &waE2E.Message{
		VideoMessage: &waE2E.VideoMessage{
			URL:           proto.String(uploaded.URL),
			Mimetype:      proto.String("video/mp4"),
			Caption:       proto.String(data.Caption),
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uploaded.FileLength),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			DirectPath:    proto.String(uploaded.DirectPath),
		},
	}, nil
}

func (s *Whatsmiau) buildAudioStatus(ctx context.Context, client statusMediaUploader, data *SendStatusRequest) (*waE2E.Message, error) {
	bytesData, err := s.fetchStatusMedia(ctx, data.Content, "audio")
	if err != nil {
		return nil, err
	}
	audioData, waveForm, secs, err := convertAudio(bytesData, 64)
	if err != nil {
		return nil, err
	}
	uploaded, err := client.Upload(ctx, audioData, whatsmeow.MediaAudio)
	if err != nil {
		return nil, err
	}
	return &waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			URL:           proto.String(uploaded.URL),
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uploaded.FileLength),
			Seconds:       proto.Uint32(uint32(secs)),
			PTT:           proto.Bool(true),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			DirectPath:    proto.String(uploaded.DirectPath),
			Waveform:      waveForm,
		},
	}, nil
}

// buildStatusMessage builds the content of a status update, uploading the media first when the
// type needs it.
func (s *Whatsmiau) buildStatusMessage(ctx context.Context, client statusMediaUploader, data *SendStatusRequest) (*waE2E.Message, error) {
	switch strings.ToLower(data.Type) {
	case "text":
		return buildTextStatus(data), nil
	case "image":
		return s.buildImageStatus(ctx, client, data)
	case "video":
		return s.buildVideoStatus(ctx, client, data)
	case "audio":
		return s.buildAudioStatus(ctx, client, data)
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidStatusType, data.Type)
	}
}

// statusClient is the part of the WhatsApp client a status update needs; *whatsmeow.Client
// implements it.
type statusClient interface {
	statusMediaUploader
	SendMessage(ctx context.Context, to types.JID, message *waE2E.Message, extra ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
}

// SendStatus posts a status update to status@broadcast. Who receives it follows the status
// privacy of the phone (my contacts, my contacts except, only share with).
//
// Errors tell refusals, after which nothing was sent, from unknown outcomes:
// ErrInvalidStatusType and ErrInvalidStatusBackground for a bad request, ErrNotLoggedIn and
// ErrInstanceNotConnected for an instance that cannot send (checked before the media is
// downloaded, so a one-time URL is not spent), ErrStatusContentUnavailable when the media could
// not be downloaded, checked or uploaded. Lookups made before the stanza is written (status
// privacy, devices, prekeys) fail with whatsmeow's IQ errors.
func (s *Whatsmiau) SendStatus(ctx context.Context, data *SendStatusRequest) (*SendStatusResponse, error) {
	if err := validateStatusContent(data.Type, data.BackgroundColor); err != nil {
		return nil, err
	}
	client, ok := s.clients.Load(data.InstanceID)
	if !ok {
		return nil, whatsmeow.ErrClientIsNil
	}
	if client.Store.GetJID().IsEmpty() {
		return nil, whatsmeow.ErrNotLoggedIn
	}
	if !client.IsConnected() || !client.IsLoggedIn() {
		return nil, ErrInstanceNotConnected
	}
	return s.sendStatus(ctx, client, data)
}

func (s *Whatsmiau) sendStatus(ctx context.Context, client statusClient, data *SendStatusRequest) (*SendStatusResponse, error) {
	msg, err := s.buildStatusMessage(ctx, client, data)
	if errors.Is(err, ErrInvalidStatusType) {
		return nil, err
	} else if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStatusContentUnavailable, err)
	}

	res, err := client.SendMessage(ctx, types.StatusBroadcastJID, msg)
	if err != nil {
		return nil, err
	}

	return &SendStatusResponse{ID: res.ID, CreatedAt: res.Timestamp}, nil
}
