package web

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminInputErrorLogsUnderlyingError(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	response := httptest.NewRecorder()
	(Handler{}).adminInputError(response, errors.New("options must be valid JSON"))

	if response.Code != 422 {
		t.Fatalf("status=%d, want 422", response.Code)
	}
	if !strings.Contains(logs.String(), "options must be valid JSON") {
		t.Fatalf("logs=%q, want underlying validation error", logs.String())
	}
}
