package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/contact"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

const (
	maxContactBody = 16 << 10
	maxLoginBody   = 8 << 10
	sessionCookie  = "odf_session"
	csrfCookie     = "odf_csrf"
)

//go:embed static/*
var assets embed.FS

type Handler struct {
	config       config.Config
	sender       contact.Sender
	verifier     contact.Verifier
	users        *auth.Store
	loginLimiter *loginLimiter
}

func NewHandler(cfg config.Config, sender contact.Sender, verifier contact.Verifier, users *auth.Store) http.Handler {
	h := Handler{config: cfg, sender: sender, verifier: verifier, users: users, loginLimiter: newLoginLimiter(4)}
	static, _ := fs.Sub(assets, "static")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /up", health)
	mux.HandleFunc("GET /api/contact-config", h.contactConfig)
	mux.HandleFunc("POST /api/contact", h.submitContact)
	mux.Handle("GET /styles.css", cache(http.FileServer(http.FS(static))))
	mux.Handle("GET /script.js", cache(http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /services", h.servicesPage)
	mux.HandleFunc("GET /shop/vending-signage", h.vendingSignagePage)
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /profile", h.userProfile)
	mux.HandleFunc("GET /client", h.clientProfile)
	mux.HandleFunc("GET /admin", h.adminLanding)
	mux.HandleFunc("GET /", h.page)
	return securityHeaders(mux)
}

func health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (h Handler) contactConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"turnstileSiteKey": h.config.TurnstileSiteKey})
}

func (h Handler) page(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.render(w, r, http.StatusOK, templates.ContactFormData{})
}

func (h Handler) servicesPage(w http.ResponseWriter, r *http.Request) {
	h.renderComponent(w, r, http.StatusOK, templates.ServicesPage(h.accountView(w, r)))
}

func (h Handler) vendingSignagePage(w http.ResponseWriter, r *http.Request) {
	h.renderComponent(w, r, http.StatusOK, templates.VendingSignagePage(h.accountView(w, r)))
}

func (h Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	account := h.accountView(w, r)
	if account.Authenticated {
		http.Redirect(w, r, "/profile", http.StatusSeeOther)
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.LoginPage(templates.LoginData{Account: account, Next: safeNext(r.URL.Query().Get("next"))}))
}

func (h Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form submission", http.StatusBadRequest)
		return
	}
	if !h.validCSRF(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	next := safeNext(r.FormValue("next"))
	client := loginClient(r)
	release, allowed := h.loginLimiter.begin(client, username)
	if !allowed {
		h.renderComponent(w, r, http.StatusTooManyRequests, templates.LoginPage(templates.LoginData{
			Account:  h.accountView(w, r),
			Username: username,
			Error:    "Too many login attempts. Try again later.",
			Next:     next,
		}))
		return
	}
	defer release()
	user, err := h.users.Authenticate(r.Context(), username, []byte(r.FormValue("password")))
	if errors.Is(err, auth.ErrInvalidCredential) {
		h.loginLimiter.failed(client, username)
		h.renderComponent(w, r, http.StatusUnauthorized, templates.LoginPage(templates.LoginData{
			Account:  h.accountView(w, r),
			Username: username,
			Error:    "Invalid username or password.",
			Next:     next,
		}))
		return
	}
	if err != nil {
		slog.Error("authenticate user", "error", err)
		http.Error(w, "could not log in", http.StatusInternalServerError)
		return
	}
	h.loginLimiter.succeeded(client, username)
	token, expires, err := h.users.CreateSession(r.Context(), user.ID)
	if err != nil {
		slog.Error("create session", "error", err)
		http.Error(w, "could not log in", http.StatusInternalServerError)
		return
	}
	h.setSessionCookie(w, token, expires)
	h.expireCookie(w, csrfCookie)
	if next == "" {
		next = "/profile"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (h Handler) logout(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
	if err := r.ParseForm(); err != nil || !h.validCSRF(r) {
		http.Error(w, "invalid form submission", http.StatusForbidden)
		return
	}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := h.users.DeleteSession(r.Context(), cookie.Value); err != nil {
			slog.Error("delete session", "error", err)
			http.Error(w, "could not log out", http.StatusInternalServerError)
			return
		}
	}
	h.expireCookie(w, sessionCookie)
	h.expireCookie(w, csrfCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h Handler) userProfile(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.UserProfilePage(account))
}

func (h Handler) clientProfile(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return
	}
	if account.Role != string(auth.RoleClient) {
		h.renderComponent(w, r, http.StatusForbidden, templates.ForbiddenPage(account))
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.ClientLandingPage(account))
}

func (h Handler) adminLanding(w http.ResponseWriter, r *http.Request) {
	account, ok := h.requireAccount(w, r)
	if !ok {
		return
	}
	if account.Role != string(auth.RoleAdmin) {
		h.renderComponent(w, r, http.StatusForbidden, templates.ForbiddenPage(account))
		return
	}
	h.renderComponent(w, r, http.StatusOK, templates.AdminLandingPage(account))
}

func (h Handler) requireAccount(w http.ResponseWriter, r *http.Request) (templates.AccountView, bool) {
	account := h.accountView(w, r)
	if account.Authenticated {
		return account, true
	}
	http.Redirect(w, r, "/login?next="+r.URL.EscapedPath(), http.StatusSeeOther)
	return templates.AccountView{}, false
}

func (h Handler) accountView(w http.ResponseWriter, r *http.Request) templates.AccountView {
	view := templates.AccountView{CSRFToken: h.ensureCSRF(w, r), Status: string(auth.StatusVisitor)}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return view
	}
	user, err := h.users.UserBySession(r.Context(), cookie.Value)
	if errors.Is(err, auth.ErrNotFound) {
		h.expireCookie(w, sessionCookie)
		return view
	}
	if err != nil {
		slog.Error("load session", "error", err)
		return view
	}
	view.Authenticated = true
	view.Username = user.Username
	view.DisplayName = user.DisplayName
	view.Role = string(user.Role)
	view.Status = string(user.CustomerStatus())
	return view
}

func (h Handler) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookie); err == nil && len(cookie.Value) == 64 {
		return cookie.Value
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		slog.Error("generate csrf token", "error", err)
		return ""
	}
	token := hex.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		Secure:   h.config.SessionCookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	return token
}

func (h Handler) validCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookie)
	value := r.FormValue("csrf_token")
	return err == nil && len(cookie.Value) == 64 && len(value) == 64 && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(value)) == 1
}

func (h Handler) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   h.config.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h Handler) expireCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.config.SessionCookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func safeNext(next string) string {
	switch next {
	case "/profile", "/client", "/admin":
		return next
	default:
		return ""
	}
}

func (h Handler) submitContact(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxContactBody)
	if err := r.ParseForm(); err != nil {
		h.renderContact(w, r, http.StatusRequestEntityTooLarge, templates.ContactFormData{Status: "The form submission was too large. Please email us instead.", State: "error"})
		return
	}

	message := contact.Message{
		Name:    contact.Clean(r.FormValue("name")),
		Email:   contact.Clean(r.FormValue("email")),
		Company: contact.Clean(r.FormValue("company")),
		Body:    contact.Clean(r.FormValue("message")),
	}
	data := templates.ContactFormData{Name: message.Name, Email: message.Email, Company: message.Company, Message: message.Body}
	if data.Errors = contact.Validate(message); len(data.Errors) != 0 {
		data.Status = "Please correct the highlighted fields."
		data.State = "error"
		h.renderContact(w, r, http.StatusUnprocessableEntity, data)
		return
	}

	// Bots that fill the hidden field receive a normal response without sending mail.
	if r.FormValue("website") == "" {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := h.verifier.Verify(ctx, r.FormValue("cf-turnstile-response"), host); err != nil {
			slog.Warn("contact challenge rejected", "error", err)
			data.Status = "Verification failed. Please try again or email us instead."
			data.State = "error"
			h.renderContact(w, r, http.StatusUnprocessableEntity, data)
			return
		}
		if err := h.sender.Send(ctx, message); err != nil {
			slog.Error("contact email failed", "error", err)
			data.Status = "Could not send your message. Please email us instead."
			data.State = "error"
			h.renderContact(w, r, http.StatusBadGateway, data)
			return
		}
	}

	h.renderContact(w, r, http.StatusOK, templates.ContactFormData{Status: "Message sent. We’ll be in touch.", State: "success"})
}

func (h Handler) renderContact(w http.ResponseWriter, r *http.Request, status int, data templates.ContactFormData) {
	data.TurnstileSiteKey = h.config.TurnstileSiteKey
	if r.Header.Get("HX-Request") == "true" {
		// HTMX only swaps successful responses by default; preserve the fragment and
		// communicate validation state in the rendered form.
		status = http.StatusOK
		h.renderComponent(w, r, status, templates.ContactForm(data))
		return
	}
	h.render(w, r, status, data)
}

func (h Handler) render(w http.ResponseWriter, r *http.Request, status int, data templates.ContactFormData) {
	data.TurnstileSiteKey = h.config.TurnstileSiteKey
	h.renderComponent(w, r, status, templates.Page(data, h.accountView(w, r)))
}

func (h Handler) renderComponent(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil && !errors.Is(err, r.Context().Err()) {
		slog.Error("render response", "error", err)
	}
}

func cache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
