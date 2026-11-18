package controllers

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"go.mau.fi/whatsmeow"
)

func TestMapStatusError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"instance", whatsmeow.ErrClientIsNil, http.StatusNotFound},
		{"logged out", whatsmeow.ErrNotLoggedIn, http.StatusNotFound},
		{"offline", whatsmiau.ErrInstanceNotConnected, http.StatusConflict},
		{"type", fmt.Errorf("%w: sticker", whatsmiau.ErrInvalidStatusType), http.StatusBadRequest},
		{"background", whatsmiau.ErrInvalidStatusBackground, http.StatusBadRequest},
		{"rate limit", fmt.Errorf("failed to get status privacy: %w", whatsmeow.ErrIQRateOverLimit), http.StatusTooManyRequests},
		{"media", fmt.Errorf("%w: %w", whatsmiau.ErrStatusContentUnavailable, errors.New("download failed")), http.StatusUnprocessableEntity},
		{"content type", fmt.Errorf("%w: %w", whatsmiau.ErrStatusContentUnavailable, whatsmiau.ErrStatusMediaType), http.StatusUnprocessableEntity},
		{"expired link", fmt.Errorf("%w: %w 410", whatsmiau.ErrStatusContentUnavailable, whatsmiau.ErrMediaHTTPStatus), http.StatusUnprocessableEntity},
		{"whatsapp refused", fmt.Errorf("%w %d", whatsmeow.ErrServerReturnedError, 479), http.StatusUnprocessableEntity},
		// Info queries run before the stanza is written: nothing was sent.
		{"privacy lookup bad request", fmt.Errorf("failed to get status privacy: %w", &whatsmeow.IQError{Code: 400, Text: "bad-request"}), http.StatusUnprocessableEntity},
		{"privacy lookup not acceptable", fmt.Errorf("failed to get status privacy: %w", whatsmeow.ErrIQNotAcceptable), http.StatusUnprocessableEntity},
		{"privacy lookup unknown error", fmt.Errorf("failed to get status privacy: %w", &whatsmeow.IQError{}), http.StatusUnprocessableEntity},
		{"devices lookup server error", fmt.Errorf("failed to get device list: %w", whatsmeow.ErrIQInternalServerError), http.StatusServiceUnavailable},
		{"prekeys lookup unavailable", fmt.Errorf("failed to fetch prekeys: %w", whatsmeow.ErrIQServiceUnavailable), http.StatusServiceUnavailable},
		{"lookup timeout", fmt.Errorf("failed to get status privacy: %w", whatsmeow.ErrIQTimedOut), http.StatusServiceUnavailable},
		{"lookup disconnected", fmt.Errorf("failed to get status privacy: %w", &whatsmeow.DisconnectedError{Action: "info query"}), http.StatusServiceUnavailable},
		// Once the stanza left, a missing answer says nothing about delivery.
		{"timeout", whatsmeow.ErrMessageTimedOut, http.StatusInternalServerError},
		{"send disconnected", &whatsmeow.DisconnectedError{Action: "message send"}, http.StatusInternalServerError},
		{"not connected", whatsmeow.ErrNotConnected, http.StatusInternalServerError},
		{"unknown", errors.New("socket closed"), http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if code, message := mapStatusError(test.err, "failed to send status"); code != test.code || message == "" {
				t.Fatalf("mapStatusError(%v) = %d %q, want %d", test.err, code, message, test.code)
			}
		})
	}
}

func TestMapStatusErrorKeepsTheRouteFallback(t *testing.T) {
	unknown := errors.New("websocket closed while waiting for the ack")
	if code, message := mapStatusError(unknown, "failed to send status"); code != http.StatusInternalServerError || message != "failed to send status" {
		t.Fatalf("sendStatus fallback = %d %q", code, message)
	}
}
