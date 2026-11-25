package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/puzpuzpuz/xsync/v4"
	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

func TestMapGroupStatusError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"instance", whatsmeow.ErrClientIsNil, http.StatusNotFound},
		{"logged out", whatsmeow.ErrNotLoggedIn, http.StatusNotFound},
		{"lib group jid", whatsmiau.ErrInvalidGroupStatusJID, http.StatusBadRequest},
		{"whatsmeow group jid", fmt.Errorf("%w (got %q)", whatsmeow.ErrGroupStatusNotGroup, "x@s.whatsapp.net"), http.StatusBadRequest},
		{"type", fmt.Errorf("%w: sticker", whatsmiau.ErrInvalidStatusType), http.StatusBadRequest},
		{"content", whatsmeow.ErrGroupStatusUnsupportedContent, http.StatusBadRequest},
		{"background", whatsmiau.ErrInvalidStatusBackground, http.StatusBadRequest},
		{"group not found", fmt.Errorf("failed to get group info: %w", whatsmeow.ErrGroupNotFound), http.StatusNotFound},
		{"not a member", fmt.Errorf("failed to get group info: %w", whatsmeow.ErrNotInGroup), http.StatusForbidden},
		{"admins only", whatsmeow.ErrGroupStatusAdminsOnly, http.StatusForbidden},
		{"rate limit", fmt.Errorf("failed to get group info: %w", whatsmeow.ErrIQRateOverLimit), http.StatusTooManyRequests},
		{"media", fmt.Errorf("%w: %w", whatsmiau.ErrStatusContentUnavailable, errors.New("download failed")), http.StatusUnprocessableEntity},
		{"whatsapp refused", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 479), http.StatusUnprocessableEntity},
		{"offline", whatsmiau.ErrInstanceNotConnected, http.StatusConflict},
		{"content type", fmt.Errorf("%w: %w", whatsmiau.ErrStatusContentUnavailable, whatsmiau.ErrStatusMediaType), http.StatusUnprocessableEntity},
		{"expired link", fmt.Errorf("%w: %w 410", whatsmiau.ErrStatusContentUnavailable, whatsmiau.ErrMediaHTTPStatus), http.StatusUnprocessableEntity},
		// Info queries run before the stanza is written: nothing was sent.
		{"group lookup bad request", fmt.Errorf("failed to get group info: %w", &whatsmeow.IQError{Code: 400, Text: "bad-request"}), http.StatusUnprocessableEntity},
		{"group lookup not acceptable", fmt.Errorf("failed to get group info: %w", whatsmeow.ErrIQNotAcceptable), http.StatusUnprocessableEntity},
		{"group lookup unknown error", fmt.Errorf("failed to get group info: %w", &whatsmeow.IQError{}), http.StatusUnprocessableEntity},
		{"group lookup not authorized", fmt.Errorf("failed to get group info: %w", whatsmeow.ErrIQNotAuthorized), http.StatusForbidden},
		{"devices lookup server error", fmt.Errorf("failed to get device list: %w", whatsmeow.ErrIQInternalServerError), http.StatusServiceUnavailable},
		{"prekeys lookup unavailable", fmt.Errorf("failed to fetch prekeys: %w", whatsmeow.ErrIQServiceUnavailable), http.StatusServiceUnavailable},
		{"lookup timeout", fmt.Errorf("failed to get group info: %w", whatsmeow.ErrIQTimedOut), http.StatusServiceUnavailable},
		{"lookup disconnected", fmt.Errorf("failed to get group info: %w", &whatsmeow.DisconnectedError{Action: "info query"}), http.StatusServiceUnavailable},
		// Once the stanza left, a missing answer says nothing about delivery.
		{"timeout", whatsmeow.ErrMessageTimedOut, http.StatusInternalServerError},
		{"send disconnected", &whatsmeow.DisconnectedError{Action: "message send"}, http.StatusInternalServerError},
		{"not connected", whatsmeow.ErrNotConnected, http.StatusInternalServerError},
		{"unknown", errors.New("socket closed"), http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if code, message := mapGroupStatusError(test.err); code != test.code || message == "" {
				t.Fatalf("mapGroupStatusError(%v) = %d %q, want %d", test.err, code, message, test.code)
			}
		})
	}
}

func TestGroupStatusResponseUsesTheEvolutionShape(t *testing.T) {
	group := types.NewJID("120363000000000001", types.GroupServer)
	res := groupStatusResponse("inst", &whatsmiau.SendGroupStatusResponse{
		ID:        "3EB0ABC",
		GroupJID:  group,
		CreatedAt: time.Unix(1763000000, 0),
	})

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	want := map[string]any{
		"key":              map[string]any{"remoteJid": "120363000000000001@g.us", "fromMe": true, "id": "3EB0ABC"},
		"status":           "sent",
		"messageType":      "groupStatusMessageV2",
		"messageTimestamp": float64(1763000000),
		"instanceId":       "inst",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %s", raw)
	}
}

func newGroupStatusTestApp(t *testing.T) *echo.Echo {
	t.Helper()
	w := &whatsmiau.Whatsmiau{}
	setUnexportedField(t, w, "clients", reflect.ValueOf(xsync.NewMap[string, *whatsmeow.Client]()))
	controller := NewMessages(nil, w)
	app := echo.New()
	app.POST("/v1/instance/:instance/message/groupStatus", controller.SendGroupStatus)
	app.POST("/v1/message/sendGroupStatus/:instance", controller.SendGroupStatus)
	return app
}

func TestSendGroupStatusRefusesBeforeSending(t *testing.T) {
	app := newGroupStatusTestApp(t)
	tests := []struct {
		name    string
		body    string
		code    int
		message string
	}{
		{"malformed json", `{"groupJid":`, http.StatusBadRequest, "failed to bind request body"},
		{"missing group", `{"type":"text","content":"x"}`, http.StatusBadRequest, "invalid request body"},
		{"missing content", `{"groupJid":"120363000000000001@g.us","type":"text"}`, http.StatusBadRequest, "invalid request body"},
		{"unknown type", `{"groupJid":"120363000000000001@g.us","type":"document","content":"x"}`, http.StatusBadRequest, "invalid request body"},
		{"font out of range", `{"groupJid":"120363000000000001@g.us","type":"text","content":"x","font":9}`, http.StatusBadRequest, "invalid request body"},
		{"phone number", `{"groupJid":"5511999999999@s.whatsapp.net","type":"text","content":"x"}`, http.StatusBadRequest, "invalid groupJid"},
		{"status broadcast", `{"groupJid":"status@broadcast","type":"text","content":"x"}`, http.StatusBadRequest, "invalid groupJid"},
		{"group without id", `{"groupJid":"@g.us","type":"text","content":"x"}`, http.StatusBadRequest, "invalid groupJid"},
		{"bad color", `{"groupJid":"120363000000000001@g.us","type":"text","content":"x","backgroundColor":"green"}`, http.StatusBadRequest, "invalid backgroundColor"},
		{"unknown instance", `{"groupJid":"120363000000000001@g.us","type":"text","content":"x"}`, http.StatusNotFound, "instance not found"},
		{"unknown instance, bare group id", `{"groupJid":"120363000000000001","type":"image","content":"https://example.com/a.jpg"}`, http.StatusNotFound, "instance not found"},
	}
	for _, test := range tests {
		for _, path := range []string{"/v1/instance/inst/message/groupStatus", "/v1/message/sendGroupStatus/inst"} {
			t.Run(test.name+" "+path, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(test.body))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				app.ServeHTTP(rec, req)

				if rec.Code != test.code {
					t.Fatalf("status = %d, want %d (body=%s)", rec.Code, test.code, rec.Body.String())
				}
				var body struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode error body: %v (body=%s)", err, rec.Body.String())
				}
				if body.Message != test.message {
					t.Fatalf("message = %q, want %q", body.Message, test.message)
				}
			})
		}
	}
}

func TestMapGroupStatusErrorKeepsTheRouteFallback(t *testing.T) {
	unknown := errors.New("websocket closed while waiting for the ack")
	if code, message := mapGroupStatusError(unknown); code != http.StatusInternalServerError || message != "failed to send group status" {
		t.Fatalf("sendGroupStatus fallback = %d %q", code, message)
	}
	// Refusals before sending are the same as on sendStatus.
	if code, _ := mapGroupStatusError(whatsmiau.ErrInstanceNotConnected); code != http.StatusConflict {
		t.Fatalf("offline instance = %d, want 409", code)
	}
}
