package whatsmiau

import (
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

func TestSendStatusText(t *testing.T) {
	sentAt := time.Unix(1794658167, 0)
	client := &fakeStatusClient{sendResp: whatsmeow.SendResponse{ID: "3EB0STATUS", Timestamp: sentAt}}

	res, err := (&Whatsmiau{}).sendStatus(context.Background(), client, &SendStatusRequest{Type: "text", Content: "Oferta do dia", BackgroundColor: "#1E8C5A", Font: 3})
	if err != nil {
		t.Fatalf("sendStatus returned an error: %v", err)
	}
	if res.ID != "3EB0STATUS" || !res.CreatedAt.Equal(sentAt) {
		t.Fatalf("response = %+v", res)
	}
	if len(client.sentTo) != 1 || client.sentTo[0] != types.StatusBroadcastJID {
		t.Fatalf("sent to %v, want status@broadcast", client.sentTo)
	}
	text := client.sent[0].GetExtendedTextMessage()
	if text.GetText() != "Oferta do dia" || text.GetBackgroundArgb() != 0xFF1E8C5A || text.GetFont() != waE2E.ExtendedTextMessage_SYSTEM_BOLD {
		t.Fatalf("text status = %v", text)
	}
}

func TestSendStatusContentFailuresAreNotSent(t *testing.T) {
	uploadErr := errors.New("media upload failed (simulated)")
	html := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("<html><body>404 Not Found</body></html>"))
	for name, test := range map[string]struct {
		client  *fakeStatusClient
		request *SendStatusRequest
		cause   error
	}{
		"upload":       {&fakeStatusClient{uploadErr: uploadErr}, &SendStatusRequest{Type: "image", Content: "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(testJPEG)}, uploadErr},
		"bad data uri": {&fakeStatusClient{}, &SendStatusRequest{Type: "image", Content: "data:image/jpeg;base64,%%%"}, nil},
		"error page":   {&fakeStatusClient{}, &SendStatusRequest{Type: "image", Content: html}, ErrStatusMediaType},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (&Whatsmiau{}).sendStatus(context.Background(), test.client, test.request)
			if !errors.Is(err, ErrStatusContentUnavailable) {
				t.Fatalf("error = %v, want ErrStatusContentUnavailable", err)
			}
			if test.cause != nil && !errors.Is(err, test.cause) {
				t.Fatalf("error = %v, want it to keep the cause %v", err, test.cause)
			}
			if len(test.client.sent) != 0 {
				t.Fatalf("a status was sent after the content failed: %v", test.client.sent)
			}
		})
	}
}

func TestSendStatusKeepsSendErrors(t *testing.T) {
	client := &fakeStatusClient{sendErr: fmt.Errorf("failed to get broadcast list members: failed to get status privacy: %w", whatsmeow.ErrIQServiceUnavailable)}

	_, err := (&Whatsmiau{}).sendStatus(context.Background(), client, &SendStatusRequest{Type: "text", Content: "x"})
	if !errors.Is(err, whatsmeow.ErrIQServiceUnavailable) {
		t.Fatalf("error = %v, want the IQ error", err)
	}
	if errors.Is(err, ErrStatusContentUnavailable) {
		t.Fatal("a send failure was reported as a content failure")
	}
}

func TestSendStatusValidatesBeforeLookingUpTheInstance(t *testing.T) {
	s := &Whatsmiau{clients: xsync.NewMap[string, *whatsmeow.Client]()}
	for request, want := range map[*SendStatusRequest]error{
		{InstanceID: "missing", Type: "sticker", Content: "x"}:                           ErrInvalidStatusType,
		{InstanceID: "missing", Type: "text", Content: "x", BackgroundColor: "green"}:    ErrInvalidStatusBackground,
		{InstanceID: "missing", Type: "text", Content: "x", BackgroundColor: "#25D366"}:  whatsmeow.ErrClientIsNil,
		{InstanceID: "missing", Type: "image", Content: "x", BackgroundColor: "ignored"}: whatsmeow.ErrClientIsNil,
	} {
		if _, err := s.SendStatus(context.Background(), request); !errors.Is(err, want) {
			t.Errorf("%+v: error = %v, want %v", *request, err, want)
		}
	}
}

func TestSendStatusChecksTheConnectionFirst(t *testing.T) {
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
				request := &SendStatusRequest{InstanceID: instance, Type: statusType, Content: srv.URL + "/image"}
				if statusType == "text" {
					request.Content = "Oferta do dia"
				}
				if _, err := s.SendStatus(context.Background(), request); !errors.Is(err, want) {
					t.Fatalf("error = %v, want %v", err, want)
				}
			})
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the media URL was fetched %d times for an instance that cannot send", hits.Load())
	}
}
