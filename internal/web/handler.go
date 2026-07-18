package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/contact"
	"github.com/bkroeze/oregon-dev-foundry/internal/templates"
)

const maxContactBody = 16 << 10

//go:embed static/*
var assets embed.FS

type Handler struct {
	config   config.Config
	sender   contact.Sender
	verifier contact.Verifier
}

func NewHandler(cfg config.Config, sender contact.Sender, verifier contact.Verifier) http.Handler {
	h := Handler{config: cfg, sender: sender, verifier: verifier}
	static, _ := fs.Sub(assets, "static")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /up", health)
	mux.HandleFunc("GET /api/contact-config", h.contactConfig)
	mux.HandleFunc("POST /api/contact", h.submitContact)
	mux.Handle("GET /styles.css", cache(http.FileServer(http.FS(static))))
	mux.Handle("GET /script.js", cache(http.FileServer(http.FS(static))))
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
	h.renderComponent(w, r, status, templates.Page(data))
}

func (h Handler) renderComponent(w http.ResponseWriter, r *http.Request, status int, component templ.Component) {
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
