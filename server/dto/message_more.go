package dto

// --- sendMedia (extended to support video) ---
// SendMediaRequest already exists in message.go and accepts mediatype.
// Mediatype values: "image" | "document" | "video"

// --- sendPtv (round video note) ---

type SendPtvRequest struct {
	InstanceID       string                `param:"instance" swaggerignore:"true"`
	Number           string                `json:"number,omitempty" validate:"required"`
	Video            string                `json:"video,omitempty" validate:"required"`
	Delay            int                   `json:"delay,omitempty" validate:"omitempty,min=0,max=300000"`
	Quoted           *MessageRequestQuoted `json:"quoted,omitempty"`
	MentionsEveryOne bool                  `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string              `json:"mentioned,omitempty"`
}

type SendPtvResponse struct {
	Key              MessageResponseKey `json:"key"`
	Status           string             `json:"status"`
	MessageType      string             `json:"messageType"`
	MessageTimestamp int                `json:"messageTimestamp"`
	InstanceId       string             `json:"instanceId"`
}

// --- sendSticker ---

type SendStickerRequest struct {
	InstanceID        string                `param:"instance" swaggerignore:"true"`
	Number            string                `json:"number,omitempty" validate:"required"`
	Sticker           string                `json:"sticker,omitempty" validate:"required"`
	Delay             int                   `json:"delay,omitempty" validate:"omitempty,min=0,max=300000"`
	Quoted            *MessageRequestQuoted `json:"quoted,omitempty"`
	MentionsEveryOne  bool                  `json:"mentionsEveryOne,omitempty"`
	Mentioned         []string              `json:"mentioned,omitempty"`
	NotConvertSticker bool                  `json:"notConvertSticker,omitempty"`
}

type SendStickerResponse struct {
	Key              MessageResponseKey `json:"key"`
	Status           string             `json:"status"`
	MessageType      string             `json:"messageType"`
	MessageTimestamp int                `json:"messageTimestamp"`
	InstanceId       string             `json:"instanceId"`
}

// --- sendLocation ---

type SendLocationRequest struct {
	InstanceID       string                `param:"instance" swaggerignore:"true"`
	Number           string                `json:"number,omitempty" validate:"required"`
	Latitude         float64               `json:"latitude,omitempty" validate:"required"`
	Longitude        float64               `json:"longitude,omitempty" validate:"required"`
	Name             string                `json:"name,omitempty"`
	Address          string                `json:"address,omitempty"`
	Delay            int                   `json:"delay,omitempty" validate:"omitempty,min=0,max=300000"`
	Quoted           *MessageRequestQuoted `json:"quoted,omitempty"`
	MentionsEveryOne bool                  `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string              `json:"mentioned,omitempty"`
}

type SendLocationResponse struct {
	Key              MessageResponseKey `json:"key"`
	Status           string             `json:"status"`
	MessageType      string             `json:"messageType"`
	MessageTimestamp int                `json:"messageTimestamp"`
	InstanceId       string             `json:"instanceId"`
}

// --- sendContact ---

type SendContactItem struct {
	FullName     string `json:"fullName,omitempty" validate:"required"`
	Wuid         string `json:"wuid,omitempty"`
	PhoneNumber  string `json:"phoneNumber,omitempty" validate:"required"`
	Organization string `json:"organization,omitempty"`
	Email        string `json:"email,omitempty"`
	URL          string `json:"url,omitempty"`
}

type SendContactRequest struct {
	InstanceID       string                `param:"instance" swaggerignore:"true"`
	Number           string                `json:"number,omitempty" validate:"required"`
	Contact          []SendContactItem     `json:"contact,omitempty" validate:"required,min=1,dive"`
	Delay            int                   `json:"delay,omitempty" validate:"omitempty,min=0,max=300000"`
	Quoted           *MessageRequestQuoted `json:"quoted,omitempty"`
	MentionsEveryOne bool                  `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string              `json:"mentioned,omitempty"`
}

type SendContactResponse struct {
	Key              MessageResponseKey `json:"key"`
	Status           string             `json:"status"`
	MessageType      string             `json:"messageType"`
	MessageTimestamp int                `json:"messageTimestamp"`
	InstanceId       string             `json:"instanceId"`
}

// --- sendPoll ---

type SendPollRequest struct {
	InstanceID      string   `param:"instance" swaggerignore:"true"`
	Number          string   `json:"number,omitempty" validate:"required"`
	Name            string   `json:"name,omitempty" validate:"required"`
	SelectableCount int      `json:"selectableCount,omitempty" validate:"min=0,max=10"`
	Values          []string `json:"values,omitempty" validate:"required,min=2,max=10"`
	// CorrectAnswer turns the poll into a single-choice quiz; it must be one of
	// values. Channels (newsletters) only.
	CorrectAnswer    string                `json:"correctAnswer,omitempty"`
	Delay            int                   `json:"delay,omitempty" validate:"omitempty,min=0,max=300000"`
	Quoted           *MessageRequestQuoted `json:"quoted,omitempty"`
	MentionsEveryOne bool                  `json:"mentionsEveryOne,omitempty"`
	Mentioned        []string              `json:"mentioned,omitempty"`
}

type SendPollResponse struct {
	Key              MessageResponseKey `json:"key"`
	Status           string             `json:"status"`
	MessageType      string             `json:"messageType"`
	MessageTimestamp int                `json:"messageTimestamp"`
	InstanceId       string             `json:"instanceId"`
}

// --- sendStatus ---

type SendStatusRequest struct {
	InstanceID      string   `param:"instance" swaggerignore:"true"`
	Type            string   `json:"type,omitempty" validate:"required,oneof=text image audio video"`
	Content         string   `json:"content,omitempty" validate:"required"`
	Caption         string   `json:"caption,omitempty"`
	BackgroundColor string   `json:"backgroundColor,omitempty"`
	Font            int      `json:"font,omitempty" validate:"omitempty,min=0,max=5"`
	StatusJidList   []string `json:"statusJidList,omitempty"`
	AllContacts     bool     `json:"allContacts,omitempty"`
}

// --- statusPrivacy ---

type StatusPrivacyRequest struct {
	InstanceID string `param:"instance" validate:"required" swaggerignore:"true"`
}

// StatusPrivacyResponse says who a status sent now would reach. Only counts are returned.
type StatusPrivacyResponse struct {
	// Privacy is the status privacy set on the phone: contacts (my contacts), blacklist (my contacts
	// except...) or whitelist (only share with...).
	Privacy string `json:"privacy" enums:"contacts,blacklist,whitelist"`
	// ListSize is the number of people in the except or only-share-with list.
	ListSize int `json:"listSize"`
	// Recipients is the number of people a status sent now goes to: the only-share-with list, or
	// the contacts saved on the phone minus the except list.
	Recipients int `json:"recipients"`
}

type SendStatusResponse struct {
	Key              MessageResponseKey `json:"key"`
	Status           string             `json:"status"`
	MessageType      string             `json:"messageType"`
	MessageTimestamp int                `json:"messageTimestamp"`
	InstanceId       string             `json:"instanceId"`
}

// --- sendGroupStatus ---

type SendGroupStatusRequest struct {
	InstanceID string `param:"instance" swaggerignore:"true"`
	// Group that receives the status, e.g. 120363000000000000@g.us. The instance's number must be a member.
	GroupJid string `json:"groupJid,omitempty" validate:"required" example:"120363000000000000@g.us"`
	Type     string `json:"type,omitempty" validate:"required,oneof=text image audio video" enums:"text,image,audio,video"`
	// Text of a text status, or the media of the other types as an http(s) URL or a data: URI.
	Content string `json:"content,omitempty" validate:"required"`
	// Caption of an image or video status; ignored for the other types.
	Caption string `json:"caption,omitempty"`
	// Background of a text status as #RRGGBB (or #AARRGGBB); ignored for the other types.
	BackgroundColor string `json:"backgroundColor,omitempty" example:"#25D366"`
	// Font of a text status, 0 to 5; ignored for the other types.
	Font int `json:"font,omitempty" validate:"omitempty,min=0,max=5"`
}

type SendGroupStatusResponse struct {
	Key              MessageResponseKey `json:"key"`
	Status           string             `json:"status" example:"sent"`
	MessageType      string             `json:"messageType" example:"groupStatusMessageV2"`
	MessageTimestamp int                `json:"messageTimestamp"`
	InstanceId       string             `json:"instanceId"`
}
