package controllers

import (
	"errors"
	"net/http"

	"github.com/verbeux-ai/whatsmiau/lib/whatsmiau"
	"go.mau.fi/whatsmeow"
)

// mapStatusError is the error contract of the status routes. It separates refusals, where
// nothing reached WhatsApp or WhatsApp answered with an error (4xx, and 503 when WhatsApp failed
// a lookup made before sending), from failures whose outcome is unknown (fallback, 500), which
// callers must not blindly retry.
//
// Info queries (status privacy, devices, prekeys) only run before the message stanza is
// written, so their errors always mean nothing was sent.
func mapStatusError(err error, fallback string) (int, string) {
	var iqErr *whatsmeow.IQError
	switch {
	case errors.Is(err, whatsmeow.ErrClientIsNil):
		return http.StatusNotFound, "instance not found"
	case errors.Is(err, whatsmeow.ErrNotLoggedIn):
		return http.StatusNotFound, "instance is not logged in"
	case errors.Is(err, whatsmiau.ErrInstanceNotConnected):
		return http.StatusConflict, "instance is not connected"
	case errors.Is(err, whatsmiau.ErrInvalidStatusType):
		return http.StatusBadRequest, "invalid status type"
	case errors.Is(err, whatsmiau.ErrInvalidStatusBackground):
		return http.StatusBadRequest, "invalid backgroundColor"
	case errors.Is(err, whatsmeow.ErrIQRateOverLimit):
		return http.StatusTooManyRequests, "rate limit exceeded, try again later"
	case errors.Is(err, whatsmiau.ErrStatusContentUnavailable):
		return http.StatusUnprocessableEntity, "failed to load the status content"
	case errors.Is(err, whatsmeow.ErrServerReturnedError):
		return http.StatusUnprocessableEntity, "WhatsApp rejected the status"
	case errors.Is(err, whatsmeow.ErrIQTimedOut), errors.Is(err, whatsmeow.ErrIQDisconnected):
		return http.StatusServiceUnavailable, "WhatsApp did not answer a lookup before sending, nothing was sent"
	case errors.As(err, &iqErr) && iqErr.Code >= http.StatusInternalServerError:
		return http.StatusServiceUnavailable, "WhatsApp failed a lookup before sending, nothing was sent"
	case errors.As(err, &iqErr):
		return http.StatusUnprocessableEntity, "WhatsApp refused a lookup before sending, nothing was sent"
	default:
		return http.StatusInternalServerError, fallback
	}
}
