package whatsmiau

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/puzpuzpuz/xsync/v4"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

var testStatusGroupJID = types.NewJID("120363000000000001", types.GroupServer)

// SendGroupStatus records a group status update.
func (f *fakeStatusClient) SendGroupStatus(_ context.Context, groupJID types.JID, inner *waE2E.Message, _ ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error) {
	f.sentTo = append(f.sentTo, groupJID)
	f.sent = append(f.sent, inner)
	return f.sendResp, f.sendErr
}

func groupStatusRequest(statusType, content string) *SendGroupStatusRequest {
	jid := testStatusGroupJID
	return &SendGroupStatusRequest{InstanceID: "inst", GroupJID: &jid, Type: statusType, Content: content}
}

func TestValidateGroupStatusRequest(t *testing.T) {
	user := types.NewJID("5511999999999", types.DefaultUserServer)
	status := types.StatusBroadcastJID
	emptyGroup := types.NewJID("", types.GroupServer)

	invalid := map[string]struct {
		request *SendGroupStatusRequest
		want    error
	}{
		"missing group":      {&SendGroupStatusRequest{Type: "text", Content: "x"}, ErrInvalidGroupStatusJID},
		"phone number":       {&SendGroupStatusRequest{GroupJID: &user, Type: "text", Content: "x"}, ErrInvalidGroupStatusJID},
		"status broadcast":   {&SendGroupStatusRequest{GroupJID: &status, Type: "text", Content: "x"}, ErrInvalidGroupStatusJID},
		"group without id":   {&SendGroupStatusRequest{GroupJID: &emptyGroup, Type: "text", Content: "x"}, ErrInvalidGroupStatusJID},
		"document":           {groupStatusRequest("document", "https://example.com/a.pdf"), ErrInvalidStatusType},
		"empty type":         {groupStatusRequest("", "x"), ErrInvalidStatusType},
		"named color":        {&SendGroupStatusRequest{GroupJID: &testStatusGroupJID, Type: "text", Content: "x", BackgroundColor: "green"}, ErrInvalidStatusBackground},
		"three digit color":  {&SendGroupStatusRequest{GroupJID: &testStatusGroupJID, Type: "text", Content: "x", BackgroundColor: "#0f0"}, ErrInvalidStatusBackground},
		"color with garbage": {&SendGroupStatusRequest{GroupJID: &testStatusGroupJID, Type: "text", Content: "x", BackgroundColor: "#25D366;"}, ErrInvalidStatusBackground},
		"five digit color":   {&SendGroupStatusRequest{GroupJID: &testStatusGroupJID, Type: "text", Content: "x", BackgroundColor: "25D36"}, ErrInvalidStatusBackground},
	}
	for name, test := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := validateGroupStatusRequest(test.request); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}

	valid := map[string]*SendGroupStatusRequest{
		"text":                 groupStatusRequest("text", "Oferta"),
		"upper case type":      groupStatusRequest("IMAGE", "https://example.com/a.jpg"),
		"video":                groupStatusRequest("video", "https://example.com/a.mp4"),
		"audio":                groupStatusRequest("audio", "https://example.com/a.ogg"),
		"rgb color":            {GroupJID: &testStatusGroupJID, Type: "text", Content: "x", BackgroundColor: "#25D366"},
		"argb color":           {GroupJID: &testStatusGroupJID, Type: "text", Content: "x", BackgroundColor: "#FF25D366"},
		"color without hash":   {GroupJID: &testStatusGroupJID, Type: "text", Content: "x", BackgroundColor: "25D366"},
		"color ignored on img": {GroupJID: &testStatusGroupJID, Type: "image", Content: "x", BackgroundColor: "green"},
	}
	for name, request := range valid {
		t.Run(name, func(t *testing.T) {
			if err := validateGroupStatusRequest(request); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSendGroupStatusText(t *testing.T) {
	sentAt := time.Unix(1763000000, 0)
	client := &fakeStatusClient{sendResp: whatsmeow.SendResponse{ID: "3EB0ABC", Timestamp: sentAt}}
	request := groupStatusRequest("text", "Oferta do dia")
	request.BackgroundColor = "#25D366"
	request.Font = 2

	res, err := (&Whatsmiau{}).sendGroupStatus(context.Background(), client, request)
	if err != nil {
		t.Fatalf("sendGroupStatus returned an error: %v", err)
	}
	if res.ID != "3EB0ABC" || !res.CreatedAt.Equal(sentAt) || res.GroupJID != testStatusGroupJID {
		t.Fatalf("unexpected response: %+v", res)
	}
	if len(client.uploads) != 0 {
		t.Fatalf("text status uploaded %d files", len(client.uploads))
	}
	if len(client.sent) != 1 || client.sentTo[0] != testStatusGroupJID {
		t.Fatalf("sent %d messages to %v, want one to %s", len(client.sent), client.sentTo, testStatusGroupJID)
	}
	text := client.sent[0].GetExtendedTextMessage()
	if text.GetText() != "Oferta do dia" {
		t.Fatalf("text = %q", text.GetText())
	}
	if text.GetBackgroundArgb() != 0xFF25D366 {
		t.Fatalf("background = %#x, want 0xFF25D366", text.GetBackgroundArgb())
	}
	if text.GetFont() != waE2E.ExtendedTextMessage_FB_SCRIPT {
		t.Fatalf("font = %v, want FB_SCRIPT", text.GetFont())
	}
}

func TestSendGroupStatusImageFromDataURI(t *testing.T) {
	client := &fakeStatusClient{sendResp: whatsmeow.SendResponse{ID: "3EB0IMG", Timestamp: time.Unix(1763000000, 0)}}
	request := groupStatusRequest("image", "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(testJPEG))
	request.Caption = "Fone por R$ 99 https://example.com/p/1"

	if _, err := (&Whatsmiau{}).sendGroupStatus(context.Background(), client, request); err != nil {
		t.Fatalf("sendGroupStatus returned an error: %v", err)
	}
	if len(client.uploads) != 1 || client.uploadTypes[0] != whatsmeow.MediaImage || !bytes.Equal(client.uploads[0], testJPEG) {
		t.Fatalf("unexpected uploads: types=%v sizes=%d", client.uploadTypes, len(client.uploads))
	}
	image := client.sent[0].GetImageMessage()
	if image == nil {
		t.Fatalf("sent message has no image: %v", client.sent[0])
	}
	if image.GetCaption() != request.Caption {
		t.Fatalf("caption = %q, want %q", image.GetCaption(), request.Caption)
	}
	if image.GetMimetype() != "image/jpeg" || image.GetDirectPath() != "/v/t62.7118-24/file.enc" || image.GetFileLength() != uint64(len(testJPEG)) {
		t.Fatalf("image fields: mimetype=%q directPath=%q length=%d", image.GetMimetype(), image.GetDirectPath(), image.GetFileLength())
	}
}

func TestSendGroupStatusVideoFromDataURI(t *testing.T) {
	client := &fakeStatusClient{}
	request := groupStatusRequest("video", "data:video/mp4;base64,"+base64.StdEncoding.EncodeToString(testMP4))
	request.Caption = "Video"

	if _, err := (&Whatsmiau{}).sendGroupStatus(context.Background(), client, request); err != nil {
		t.Fatalf("sendGroupStatus returned an error: %v", err)
	}
	if len(client.uploadTypes) != 1 || client.uploadTypes[0] != whatsmeow.MediaVideo {
		t.Fatalf("upload types = %v, want [video]", client.uploadTypes)
	}
	if video := client.sent[0].GetVideoMessage(); video == nil || video.GetCaption() != "Video" || video.GetMimetype() != "video/mp4" {
		t.Fatalf("unexpected video message: %v", client.sent[0])
	}
}

func TestSendGroupStatusContentFailuresAreNotSent(t *testing.T) {
	uploadErr := errors.New("media upload failed (simulated)")
	for name, test := range map[string]struct {
		client  *fakeStatusClient
		request *SendGroupStatusRequest
		cause   error
	}{
		"upload": {
			client:  &fakeStatusClient{uploadErr: uploadErr},
			request: groupStatusRequest("image", "data:image/jpeg;base64,"+base64.StdEncoding.EncodeToString(testJPEG)),
			cause:   uploadErr,
		},
		"bad data uri": {
			client:  &fakeStatusClient{},
			request: groupStatusRequest("image", "data:image/jpeg;base64,%%%"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (&Whatsmiau{}).sendGroupStatus(context.Background(), test.client, test.request)
			if !errors.Is(err, ErrStatusContentUnavailable) {
				t.Fatalf("error = %v, want ErrStatusContentUnavailable", err)
			}
			if test.cause != nil && !errors.Is(err, test.cause) {
				t.Fatalf("error = %v, want it to keep the cause %v", err, test.cause)
			}
			if len(test.client.sent) != 0 {
				t.Fatalf("a message was sent after the content failed: %v", test.client.sent)
			}
		})
	}
}

func TestSendGroupStatusKeepsSendErrors(t *testing.T) {
	client := &fakeStatusClient{sendErr: fmt.Errorf("failed to get group info: %w", whatsmeow.ErrNotInGroup)}

	_, err := (&Whatsmiau{}).sendGroupStatus(context.Background(), client, groupStatusRequest("text", "x"))
	if !errors.Is(err, whatsmeow.ErrNotInGroup) {
		t.Fatalf("error = %v, want ErrNotInGroup", err)
	}
	if errors.Is(err, ErrStatusContentUnavailable) {
		t.Fatal("a send failure was reported as a content failure")
	}
}

func TestSendGroupStatusValidatesBeforeLookingUpTheInstance(t *testing.T) {
	s := &Whatsmiau{clients: xsync.NewMap[string, *whatsmeow.Client]()}
	user := types.NewJID("5511999999999", types.DefaultUserServer)

	if _, err := s.SendGroupStatus(context.Background(), &SendGroupStatusRequest{InstanceID: "missing", GroupJID: &user, Type: "text", Content: "x"}); !errors.Is(err, ErrInvalidGroupStatusJID) {
		t.Fatalf("invalid group error = %v, want ErrInvalidGroupStatusJID", err)
	}
	request := groupStatusRequest("text", "x")
	request.InstanceID = "missing"
	if _, err := s.SendGroupStatus(context.Background(), request); !errors.Is(err, whatsmeow.ErrClientIsNil) {
		t.Fatalf("missing instance error = %v, want ErrClientIsNil", err)
	}
}

func TestBuildStatusMessageRejectsUnknownType(t *testing.T) {
	_, err := (&Whatsmiau{}).buildStatusMessage(context.Background(), &fakeStatusClient{}, &SendStatusRequest{Type: "sticker", Content: "x"})
	if !errors.Is(err, ErrInvalidStatusType) {
		t.Fatalf("error = %v, want ErrInvalidStatusType", err)
	}
	if err.Error() != "invalid status type: sticker" {
		t.Fatalf("error message = %q, the regular status route keeps this text", err.Error())
	}
}

func TestSendGroupStatusChecksTheConnectionFirst(t *testing.T) {
	srv, hits := newStatusMediaServer(t)
	paired := types.NewJID("5511999999999", types.DefaultUserServer)
	clients := xsync.NewMap[string, *whatsmeow.Client]()
	clients.Store("logged-out", &whatsmeow.Client{Store: &store.Device{}})
	clients.Store("no-store", &whatsmeow.Client{})
	clients.Store("offline", &whatsmeow.Client{Store: &store.Device{ID: &paired}})
	s := &Whatsmiau{clients: clients, httpClient: srv.Client()}

	for instance, want := range map[string]error{
		"logged-out": whatsmeow.ErrNotLoggedIn,
		"no-store":   whatsmeow.ErrNotLoggedIn,
		"offline":    ErrInstanceNotConnected,
	} {
		for _, statusType := range []string{"text", "image"} {
			t.Run(instance+" "+statusType, func(t *testing.T) {
				request := groupStatusRequest(statusType, srv.URL+"/image")
				if statusType == "text" {
					request.Content = "Oferta do dia"
				}
				request.InstanceID = instance
				if _, err := s.SendGroupStatus(context.Background(), request); !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			})
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the media URL was fetched %d times for an instance that cannot send", hits.Load())
	}
}

func TestSendGroupStatusDoesNotPostAnExpiredLink(t *testing.T) {
	srv, _ := newStatusMediaServer(t)
	client := &fakeStatusClient{sendResp: whatsmeow.SendResponse{ID: "3EB0IMG"}}
	request := groupStatusRequest("image", srv.URL+"/expired")
	request.Caption = "Fone por R$ 99 https://example.com/p/1"

	_, err := (&Whatsmiau{httpClient: srv.Client()}).sendGroupStatus(context.Background(), client, request)
	if !errors.Is(err, ErrStatusContentUnavailable) || !errors.Is(err, ErrMediaHTTPStatus) {
		t.Fatalf("error = %v, want ErrStatusContentUnavailable wrapping ErrMediaHTTPStatus", err)
	}
	if len(client.uploads) != 0 || len(client.sent) != 0 {
		t.Fatalf("uploads=%d sent=%d, want nothing", len(client.uploads), len(client.sent))
	}
}
