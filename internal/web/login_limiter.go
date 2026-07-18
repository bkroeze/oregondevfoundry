package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	loginFailureLimit  = 5
	loginBlockDuration = 5 * time.Minute
	loginAttemptWindow = 15 * time.Minute
	maxLoginClients    = 4096
)

type loginAttempt struct {
	failures     int
	lastFailure  time.Time
	blockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	active   chan struct{}
	now      func() time.Time
}

func newLoginLimiter(maxConcurrent int) *loginLimiter {
	return &loginLimiter{
		attempts: make(map[string]loginAttempt),
		active:   make(chan struct{}, maxConcurrent),
		now:      time.Now,
	}
}

func (l *loginLimiter) begin(client, username string) (func(), bool) {
	now := l.now()
	ipKey, accountKey := loginKeys(client, username)
	l.mu.Lock()
	for _, key := range []string{ipKey, accountKey} {
		attempt, found := l.attempts[key]
		if found && now.Sub(attempt.lastFailure) >= loginAttemptWindow {
			delete(l.attempts, key)
			continue
		}
		if found && now.Before(attempt.blockedUntil) {
			l.mu.Unlock()
			return nil, false
		}
	}
	if len(l.attempts) >= maxLoginClients {
		for key, candidate := range l.attempts {
			if now.Sub(candidate.lastFailure) >= loginAttemptWindow {
				delete(l.attempts, key)
			}
		}
	}
	l.mu.Unlock()

	select {
	case l.active <- struct{}{}:
		return func() { <-l.active }, true
	default:
		return nil, false
	}
}

func (l *loginLimiter) failed(client, username string) {
	now := l.now()
	ipKey, accountKey := loginKeys(client, username)
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range []string{ipKey, accountKey} {
		attempt, found := l.attempts[key]
		if !found && len(l.attempts) >= maxLoginClients {
			continue
		}
		if now.Sub(attempt.lastFailure) >= loginAttemptWindow {
			attempt.failures = 0
		}
		attempt.failures++
		attempt.lastFailure = now
		if attempt.failures >= loginFailureLimit {
			attempt.blockedUntil = now.Add(loginBlockDuration)
		}
		l.attempts[key] = attempt
	}
}

func (l *loginLimiter) succeeded(client, username string) {
	_, accountKey := loginKeys(client, username)
	l.mu.Lock()
	delete(l.attempts, accountKey)
	l.mu.Unlock()
}

func loginKeys(client, username string) (string, string) {
	return "ip:" + client, "account:" + client + "\x00" + strings.ToLower(strings.TrimSpace(username))
}

func loginClient(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}
