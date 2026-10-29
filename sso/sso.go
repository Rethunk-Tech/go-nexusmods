// Package sso signs in to Nexus Mods without a pasted key. Legacy SSO is Nexus's websocket handshake and returns an
// API key; the OAuth 2 PKCE flow is a loopback redirect and returns tokens. Each needs what Nexus issues an app: a
// slug for the first, a client id and a registered callback address for the second.
package sso

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/coder/websocket"
)

const (
	// SocketURL is Nexus's SSO websocket.
	SocketURL = "wss://sso.nexusmods.com"
	// PageURL is the page the user approves the sign-in on.
	PageURL = "https://www.nexusmods.com/sso"

	defaultTimeout = 5 * time.Minute
	maxReconnects  = 3
)

// Errors a caller can tell apart: the user gave up, or never approved in time.
var (
	ErrTimeout       = errors.New("nexus sign-in timed out; the browser page was not approved")
	ErrNotConfigured = errors.New("nexus single sign-on is not configured")
)

// State is a stage of the sign-in a UI can show.
type State string

// Sign-in stages. Waiting means the browser page is open; Connected means the socket accepted the request.
const (
	Connected State = "connected"
	Waiting   State = "waiting-browser"
)

// Legacy configures one legacy SSO run. Slug is the application slug Nexus issued; zero values elsewhere use the
// production endpoints and timeout.
type Legacy struct {
	Slug      string
	SocketURL string
	PageURL   string
	Timeout   time.Duration
	// RetryDelay is the pause before redialing a dropped socket.
	RetryDelay time.Duration
	// OpenBrowser opens the approval page.
	OpenBrowser func(url string) error
	// OnState reports progress; it may be nil.
	OnState func(State)
}

type reply struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    struct {
		ConnectionToken string `json:"connection_token"`
		APIKey          string `json:"api_key"`
	} `json:"data"`
}

// Run returns the API key the user approved. A dropped socket is redialed with the connection token so the
// pending approval survives; cancelling ctx or the timeout ends the run.
func (l Legacy) Run(ctx context.Context) (string, error) {
	if l.Slug == "" {
		return "", ErrNotConfigured
	}
	l.SocketURL = cmp.Or(l.SocketURL, SocketURL)
	l.PageURL = cmp.Or(l.PageURL, PageURL)
	l.Timeout = cmp.Or(l.Timeout, defaultTimeout)
	l.RetryDelay = cmp.Or(l.RetryDelay, time.Second)
	runCtx, cancel := context.WithTimeout(ctx, l.Timeout)
	defer cancel()

	id := rand.Text()
	var token string
	opened := false
	for attempt := 0; ; attempt++ {
		key, err := l.session(runCtx, id, &token, &opened)
		if err == nil {
			return key, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if runCtx.Err() != nil {
			return "", ErrTimeout
		}
		var refused refusal
		if errors.As(err, &refused) || attempt >= maxReconnects {
			return "", err
		}
		select {
		case <-time.After(l.RetryDelay):
		case <-runCtx.Done():
		}
	}
}

// refusal is a failure a redial cannot fix: Nexus answering success:false, or no browser to open.
type refusal struct{ msg string }

func (r refusal) Error() string { return "nexus sign-in failed: " + r.msg }

func (l Legacy) session(ctx context.Context, id string, token *string, opened *bool) (string, error) {
	conn, resp, err := websocket.Dial(ctx, l.SocketURL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.CloseNow() }()

	hello := map[string]any{"id": id, "token": nil, "protocol": 2}
	if *token != "" {
		hello["token"] = *token
	}
	b, _ := json.Marshal(hello)
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		return "", err
	}
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return "", err
		}
		key, err := l.handle(raw, id, token, opened)
		if err != nil || key != "" {
			return key, err
		}
	}
}

// handle acts on one socket message: it remembers the connection token, opens the approval page once, and returns
// the API key when the message carries it.
func (l Legacy) handle(raw []byte, id string, token *string, opened *bool) (string, error) {
	var r reply
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("nexus sent an unreadable sign-in message: %w", err)
	}
	if !r.Success {
		return "", refusal{r.Error}
	}
	if r.Data.ConnectionToken != "" {
		*token = r.Data.ConnectionToken
		l.emit(Connected)
		if !*opened {
			if err := l.openPage(id); err != nil {
				return "", refusal{err.Error()}
			}
			*opened = true
			l.emit(Waiting)
		}
	}
	return r.Data.APIKey, nil
}

func (l Legacy) openPage(id string) error {
	if l.OpenBrowser == nil {
		return errors.New("no browser to open")
	}
	q := url.Values{"id": {id}, "application": {l.Slug}}
	return l.OpenBrowser(l.PageURL + "?" + q.Encode())
}

func (l Legacy) emit(s State) {
	if l.OnState != nil {
		l.OnState(s)
	}
}
