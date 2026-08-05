package web

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/bkroeze/oregon-dev-foundry/internal/commerce"
)

func (h Handler) captureReferral(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(referralCookie); errors.Is(err, http.ErrNoCookie) {
			sourceURL, host, ok := externalReferrer(r.Referer(), r.URL, r.Host)
			if ok {
				referral, createErr := h.commerce.FindOrCreateReferral(r.Context(), sourceURL, host)
				if createErr != nil {
					slog.Error("capture referral", "error", createErr)
				} else {
					h.setReferralCookie(w, referral.Token)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h Handler) setReferralCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: referralCookie, Value: token, Path: "/", Expires: time.Now().Add(365 * 24 * time.Hour),
		MaxAge: 365 * 24 * 60 * 60, HttpOnly: true, Secure: h.config.SessionCookieSecure, SameSite: http.SameSiteLaxMode,
	})
}

func (h Handler) referralID(r *http.Request) *int64 {
	cookie, err := r.Cookie(referralCookie)
	if err != nil {
		return nil
	}
	referral, err := h.commerce.ReferralByToken(r.Context(), cookie.Value)
	if err != nil {
		if !errors.Is(err, commerce.ErrNotFound) {
			slog.Error("load referral", "error", err)
		}
		return nil
	}
	return &referral.ID
}

func (h Handler) associateCurrentReferral(r *http.Request, userID int64) {
	if referralID := h.referralID(r); referralID != nil {
		if err := h.commerce.AssociateReferralWithUser(r.Context(), userID, *referralID); err != nil {
			slog.Error("associate referral with user", "error", err)
		}
	}
}
