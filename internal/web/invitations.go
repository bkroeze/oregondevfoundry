package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

func (h Handler) invitationPage(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	invitation, user, err := h.users.InvitationByToken(r.Context(), token)
	if errors.Is(err, auth.ErrInvitationNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.commerceError(w, "load account invitation", err)
		return
	}
	account := h.accountView(w, r)
	h.renderComponent(w, r, http.StatusOK, templates.InvitationPage(templates.InvitationData{
		Account: account, Email: invitation.Email, Name: user.DisplayName, Username: suggestedUsername(invitation.Email),
		Token: token, Action: "/account/invite", CSRFToken: account.CSRFToken, Errors: map[string]string{}, AlreadyAccepted: invitation.UsedAt != nil,
	}))
}

func (h Handler) completeInvitation(w http.ResponseWriter, r *http.Request) {
	if !h.parseCommerceForm(w, r) {
		return
	}
	token := strings.TrimSpace(r.FormValue("invitation_token"))
	invitation, _, err := h.users.InvitationByToken(r.Context(), token)
	if errors.Is(err, auth.ErrInvitationNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.commerceError(w, "load account invitation", err)
		return
	}
	name, username := strings.TrimSpace(r.FormValue("name")), strings.TrimSpace(r.FormValue("username"))
	password, confirmation := r.FormValue("password"), r.FormValue("password_confirmation")
	problems := make(map[string]string)
	if name == "" || len(name) > 120 {
		problems["name"] = "Enter a name no longer than 120 characters."
	}
	if len(username) < 3 || len(username) > 64 {
		problems["username"] = "Use a username between 3 and 64 characters."
	}
	if len(password) < 12 || len(password) > 72 {
		problems["password"] = "Use a password between 12 and 72 characters."
	}
	if password != confirmation {
		problems["password_confirmation"] = "Passwords do not match."
	}
	if len(problems) != 0 {
		h.renderInvitationError(w, r, token, invitation.Email, name, username, problems, "Check the account details and try again.")
		return
	}
	user, err := h.users.CompleteInvitationProfile(r.Context(), token, username, name, []byte(password))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrUsernameExists):
			problems["username"] = "That username is already in use."
		case errors.Is(err, auth.ErrInvitationUsed):
			h.renderInvitationError(w, r, token, invitation.Email, name, username, problems, "This invitation has already been accepted.")
			return
		default:
			slog.Error("complete purchase invitation", "error", err)
			problems["username"] = "Use only letters, numbers, dots, underscores, or hyphens."
		}
		h.renderInvitationError(w, r, token, invitation.Email, name, username, problems, "Could not create the account.")
		return
	}
	sessionToken, expires, err := h.users.CreateSession(r.Context(), user.ID)
	if err != nil {
		h.commerceError(w, "create invited user session", err)
		return
	}
	h.setSessionCookie(w, sessionToken, expires)
	h.expireCookie(w, csrfCookie)
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

func (h Handler) renderInvitationError(w http.ResponseWriter, r *http.Request, token, email, name, username string, problems map[string]string, message string) {
	account := h.accountView(w, r)
	h.renderComponent(w, r, http.StatusUnprocessableEntity, templates.InvitationPage(templates.InvitationData{
		Account: account, Email: email, Name: name, Username: username, Token: token, Action: "/account/invite",
		CSRFToken: account.CSRFToken, Errors: problems, Error: message,
	}))
}

func suggestedUsername(email string) string {
	local := strings.SplitN(strings.TrimSpace(email), "@", 2)[0]
	if len(local) < 3 {
		return "customer"
	}
	return local
}
