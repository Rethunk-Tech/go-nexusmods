package sso

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/gofrs/flock"
)

const (
	// AuthBase is Nexus's OAuth2 server.
	AuthBase = "https://users.nexusmods.com/oauth"
	// Scope is public, which authorises the REST and GraphQL calls, with openid and profile to identify the user.
	Scope = "public openid profile"
	// CallbackPath is the path Nexus redirects to. The redirect URI is "http://" + OAuth.Addr + CallbackPath.
	CallbackPath = "/oauth/callback"

	refreshMargin = time.Minute
	maxTokenBody  = 64 << 10
	// maxExpiresIn is a year in seconds: a lifetime beyond it is a broken answer, and the multiplication to a
	// time.Duration would overflow for a far larger one.
	maxExpiresIn = 365 * 24 * 60 * 60

	defaultDoneText = "Signed in. You can close this tab."
	defaultFailText = "Sign-in did not finish. You can close this tab."
)

var (
	// ErrRevoked means Nexus refused the stored refresh token, so the account has to sign in again.
	ErrRevoked = errors.New("nexus sign-in expired")
	// ErrNoTokens is what a TokenStore returns from Load when it holds no grant.
	ErrNoTokens = errors.New("nexus sign-in: no stored tokens")
	// ErrPortBusy means the callback address is in use. Authorize wraps it with the address; any other listen
	// failure keeps its own cause.
	ErrPortBusy = errors.New("nexus sign-in: the callback address is in use")
)

// refreshMu keeps concurrent callers from spending one refresh token twice; Nexus rotates it on use. It is
// process-wide because callers build an OAuth value per call, so a per-value lock would serialise nothing. Different
// stores in one process wait on each other, which costs only time.
var refreshMu sync.Mutex

// Tokens are the OAuth2 grant; the access token goes in the Authorization header of every Nexus call. It has no
// JSON tags: the module never encodes it, so a TokenStore owns its stored shape.
type Tokens struct {
	Access  string
	Refresh string
	Expires time.Time
}

// TokenStore keeps the grant between runs. Load returns ErrNoTokens when there is none; any other error (a keyring
// still waiting on an unlock prompt) is returned to Fresh's caller unchanged. A store that cannot hold the access
// token may save it empty: Fresh treats an empty Access as expired and refreshes.
type TokenStore interface {
	Load() (Tokens, error)
	Save(Tokens) error
}

// OAuth configures one PKCE run. Zero values use the production endpoints and timeout, except Addr, which Nexus
// registers as a fixed callback and which has no default.
type OAuth struct {
	ClientID string
	// Addr is the loopback host:port registered with Nexus as the callback. It is required: a random port would
	// fail only at Nexus.
	Addr        string
	AuthBase    string
	Timeout     time.Duration
	HTTP        *http.Client
	OpenBrowser func(url string) error
	OnState     func(State)
	Now         func() time.Time
	// Store is required by Fresh.
	Store TokenStore
	// LockFile, when set, is an exclusive lock file Fresh holds across load, refresh and save, so two processes
	// sharing one Store cannot spend the same rotated refresh token. Empty serialises callers of this process only.
	LockFile string
	// DoneText is what the browser tab shows after a granted sign-in; empty means a neutral sentence.
	DoneText string
	// FailText is what it shows after a denied or failed one; empty means a neutral sentence.
	FailText string
}

func (o OAuth) base() string { return cmp.Or(o.AuthBase, AuthBase) }

// checkBase refuses an endpoint override that would send a token in cleartext; loopback is allowed for local servers.
func (o OAuth) checkBase() error { return requireTLS(o.base(), "https") }

func (o OAuth) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o OAuth) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// listen opens the callback listener, so the browser is never opened for a callback that cannot arrive.
func (o OAuth) listen(ctx context.Context) (net.Listener, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", o.Addr)
	if err == nil {
		return ln, nil
	}
	if isAddrInUse(err) {
		return nil, fmt.Errorf("%w: %s", ErrPortBusy, o.Addr)
	}
	return nil, fmt.Errorf("could not listen on %s: %w", o.Addr, err)
}

// shutdownGrace bounds how long a finished sign-in waits for the browser to receive its page.
const shutdownGrace = 2 * time.Second

type callbackResult struct{ code, err string }

// callback answers the redirect Nexus sends the browser to, for the state this run issued, and passes its outcome on.
func (o OAuth) callback(state string, got chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != CallbackPath || q.Get("state") != state {
			http.NotFound(w, r)
			return
		}
		res := callbackResult{code: q.Get("code"), err: q.Get("error")}
		if res.code == "" && res.err == "" {
			res.err = "no code returned"
		}
		if res.err != "" {
			_, _ = w.Write([]byte(cmp.Or(o.FailText, defaultFailText)))
		} else {
			_, _ = w.Write([]byte(cmp.Or(o.DoneText, defaultDoneText)))
		}
		select {
		case got <- res:
		default:
		}
	})
}

// Authorize runs the loopback authorization-code flow and returns the tokens.
func (o OAuth) Authorize(ctx context.Context) (Tokens, error) {
	if o.ClientID == "" {
		return Tokens{}, ErrNotConfigured
	}
	if o.Addr == "" {
		return Tokens{}, errors.New("nexus sign-in: no callback address")
	}
	if o.OpenBrowser == nil {
		return Tokens{}, errors.New("no browser to open")
	}
	if err := o.checkBase(); err != nil {
		return Tokens{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, cmp.Or(o.Timeout, defaultTimeout))
	defer cancel()

	ln, err := o.listen(runCtx)
	if err != nil {
		return Tokens{}, err
	}
	redirect := "http://" + ln.Addr().String() + CallbackPath
	verifier := rand.Text() + rand.Text()
	state := rand.Text()
	sum := sha256.Sum256([]byte(verifier))

	got := make(chan callbackResult, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: o.callback(state, got)}
	go func() { _ = srv.Serve(ln) }()
	// Shutdown, not Close: the callback signals before its page reaches the browser, and Close would reset it.
	defer func() {
		done, stop := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer stop()
		_ = srv.Shutdown(done)
	}()

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {o.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {Scope},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
	if err := o.OpenBrowser(o.base() + "/authorize?" + q.Encode()); err != nil {
		return Tokens{}, err
	}
	if o.OnState != nil {
		o.OnState(Waiting)
	}
	select {
	case res := <-got:
		if res.err != "" {
			return Tokens{}, fmt.Errorf("nexus sign-in failed: %s", res.err)
		}
		return o.exchange(runCtx, url.Values{
			"grant_type": {"authorization_code"}, "code": {res.code}, "redirect_uri": {redirect}, "code_verifier": {verifier},
		})
	case <-runCtx.Done():
		if ctx.Err() != nil {
			return Tokens{}, ctx.Err()
		}
		return Tokens{}, ErrTimeout
	}
}

func (o OAuth) exchange(ctx context.Context, form url.Values) (Tokens, error) {
	if err := o.checkBase(); err != nil {
		return Tokens{}, err
	}
	form.Set("client_id", o.ClientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base()+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// A 307 or 308 would re-send the form, refresh token included, to wherever it points.
	body, err := httpkit.DoCapped(httpkit.WithRedirect(o.client(), httpkit.NoFollow), req, maxTokenBody, nil)
	if se, ok := errors.AsType[*httpkit.StatusError](err); ok && form.Get("grant_type") == "refresh_token" &&
		(se.Code == http.StatusBadRequest || se.Code == http.StatusUnauthorized) {
		return Tokens{}, fmt.Errorf("nexus token endpoint answered %s: %w", se.Status, ErrRevoked)
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("nexus token endpoint: %w", err)
	}
	var raw struct {
		Access    string `json:"access_token"`
		Refresh   string `json:"refresh_token"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Tokens{}, err
	}
	if raw.Access == "" {
		return Tokens{}, errors.New("nexus token endpoint returned no access token")
	}
	if raw.ExpiresIn < 0 || raw.ExpiresIn > maxExpiresIn {
		return Tokens{}, fmt.Errorf("nexus token endpoint returned expires_in %d", raw.ExpiresIn)
	}
	return Tokens{Access: raw.Access, Refresh: raw.Refresh, Expires: o.now().Add(time.Duration(raw.ExpiresIn) * time.Second)}, nil
}

// Fresh returns the stored tokens, refreshing and re-saving them when they expire within a minute or hold no access
// token.
func (o OAuth) Fresh(ctx context.Context) (Tokens, error) {
	t, err := o.Store.Load()
	if err != nil {
		return Tokens{}, err
	}
	if o.usable(t) {
		return t, nil
	}
	refreshMu.Lock()
	defer refreshMu.Unlock()
	unlock, err := o.lockFile(ctx)
	if err != nil {
		return Tokens{}, err
	}
	defer unlock()
	if t, err = o.Store.Load(); err != nil {
		return Tokens{}, err
	}
	t = o.recovered(t)
	if o.usable(t) {
		return t, nil
	}
	if t.Refresh == "" {
		return Tokens{}, errors.New("nexus access token expired and no refresh token is stored")
	}
	next, err := o.exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t.Refresh}})
	if err != nil {
		return Tokens{}, err
	}
	next.Refresh = cmp.Or(next.Refresh, t.Refresh)
	return next, o.save(t.Refresh, next)
}

// lockPoll is how often a Fresh waiting on another process's lock tries again.
const lockPoll = 50 * time.Millisecond

// lockFile takes the cross-process lock, or does nothing when LockFile is unset.
func (o OAuth) lockFile(ctx context.Context) (func(), error) {
	if o.LockFile == "" {
		return func() {}, nil
	}
	l := flock.New(o.LockFile)
	if _, err := l.TryLockContext(ctx, lockPoll); err != nil {
		return nil, fmt.Errorf("nexus sign-in: lock %s: %w", o.LockFile, err)
	}
	return func() { _ = l.Unlock() }, nil
}

// unsaved is the last refresh whose Save failed: Nexus has already spent the stored refresh token, so the new grant
// is kept here and offered to the next Fresh until a Save takes it.
var unsaved struct {
	spent  string
	tokens Tokens
}

// save stores next, retrying once, and keeps it in memory when the store refuses both times.
func (o OAuth) save(spent string, next Tokens) error {
	err := o.Store.Save(next)
	if err != nil {
		err = o.Store.Save(next)
	}
	if err != nil {
		unsaved.spent, unsaved.tokens = spent, next
		return fmt.Errorf("nexus sign-in refreshed but the new tokens were not stored: %w", err)
	}
	unsaved.spent = ""
	return nil
}

// recovered swaps the stored grant for the unsaved one that replaced it.
func (o OAuth) recovered(t Tokens) Tokens {
	if unsaved.spent == "" || unsaved.spent != t.Refresh {
		return t
	}
	if o.Store.Save(unsaved.tokens) == nil {
		unsaved.spent = ""
	}
	return unsaved.tokens
}

func (o OAuth) usable(t Tokens) bool {
	return t.Access != "" && o.now().Add(refreshMargin).Before(t.Expires)
}

// wsaEADDRINUSE is Windows' WSAEADDRINUSE, which syscall.EADDRINUSE does not match there.
const wsaEADDRINUSE = syscall.Errno(10048)

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, wsaEADDRINUSE)
}
