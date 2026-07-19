package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bkroeze/oregon-dev-foundry/internal/auth"
	"github.com/bkroeze/oregon-dev-foundry/internal/config"
	"github.com/bkroeze/oregon-dev-foundry/internal/contact"
	"github.com/bkroeze/oregon-dev-foundry/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	users, err := auth.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer users.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	sender := contact.NewMailgunSender(cfg.MailgunDomain, cfg.MailgunAPIKey, cfg.MailgunRegion, cfg.ContactFrom, cfg.ContactTo)
	verifier := contact.NewTurnstileVerifier(cfg.TurnstileSecretKey, client)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           web.NewHandler(cfg, sender, verifier, users),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("Oregon Dev Foundry listening", "address", cfg.Address)
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}

	err = <-serveErr
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
