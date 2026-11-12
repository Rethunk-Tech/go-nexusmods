package sso

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
)

// memStore is a TokenStore in memory; the module never touches a real keyring.
type memStore struct {
	t       Tokens
	has     bool
	saveErr error
}

func (m *memStore) Load() (Tokens, error) {
	if !m.has {
		return Tokens{}, ErrNoTokens
	}
	return m.t, nil
}

func (m *memStore) Save(t Tokens) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.t, m.has = t, true
	return nil
}

func TestOAuthPKCEAndRefresh(t *testing.T) {
	var challenge string
	var forms []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		forms = append(forms, r.PostForm)
		if r.PostForm.Get("grant_type") == "authorization_code" {
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"A1","refresh_token":"R1","expires_in":30}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"A2","expires_in":3600}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &memStore{}
	o := OAuth{ClientID: "cid", AuthBase: srv.URL, Now: func() time.Time { return now }, Addr: "127.0.0.1:0", Store: store}
	o.OpenBrowser = func(raw string) error {
		u, _ := url.Parse(raw)
		q := u.Query()
		challenge = q.Get("code_challenge")
		if !strings.HasSuffix(q.Get("redirect_uri"), CallbackPath) || q.Get("scope") != "public openid profile" {
			t.Errorf("redirect %q scope %q", q.Get("redirect_uri"), q.Get("scope"))
		}
		if q.Get("client_id") != "cid" || q.Get("code_challenge_method") != "S256" {
			t.Errorf("authorize query %v", q)
		}
		go func() {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, q.Get("redirect_uri")+"?code=CODE&state="+q.Get("state"), nil)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	tok, err := o.Authorize(t.Context())
	if err != nil || tok.Access != "A1" || tok.Refresh != "R1" || !tok.Expires.Equal(now.Add(30*time.Second)) {
		t.Fatalf("tokens %+v err %v", tok, err)
	}
	if err := store.Save(tok); err != nil {
		t.Fatal(err)
	}
	got, err := o.Fresh(t.Context())
	if err != nil || got.Access != "A2" || got.Refresh != "R1" {
		t.Fatalf("refreshed %+v err %v", got, err)
	}
	if forms[len(forms)-1].Get("refresh_token") != "R1" {
		t.Fatalf("refresh form %v", forms[len(forms)-1])
	}
	if stored, _ := store.Load(); stored.Access != "A2" {
		t.Fatalf("not re-saved: %+v", stored)
	}
}

func TestOAuthTimeoutAndDeniedCallback(t *testing.T) {
	o := OAuth{ClientID: "c", Timeout: 50 * time.Millisecond, OpenBrowser: func(string) error { return nil }, Addr: "127.0.0.1:0", FailText: "NOPE", DoneText: "YES"}
	if _, err := o.Authorize(t.Context()); !errors.Is(err, ErrTimeout) {
		t.Fatalf("timeout: %v", err)
	}
	o.Timeout = time.Second
	page := make(chan string, 1)
	o.OpenBrowser = func(raw string) error {
		u, _ := url.Parse(raw)
		go func() {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u.Query().Get("redirect_uri")+"?error=access_denied&state="+u.Query().Get("state"), nil)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				page <- string(b)
			}
		}()
		return nil
	}
	if _, err := o.Authorize(t.Context()); err == nil {
		t.Fatal("denied callback succeeded")
	}
	if p := <-page; p != "NOPE" {
		t.Fatalf("denied sign-in page says %q", p)
	}
}

func TestExchangeRefusesAnOversizedTokenBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"`))
		_, _ = w.Write(bytes.Repeat([]byte("A"), maxTokenBody))
		_, _ = w.Write([]byte(`","refresh_token":"R","expires_in":60}`))
	}))
	defer srv.Close()
	o := OAuth{ClientID: "cid", AuthBase: srv.URL}
	_, err := o.exchange(t.Context(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"R"}})
	if err == nil {
		t.Fatal("oversized token body was accepted")
	}
}

func TestBusyPortFailsWithoutFallingBack(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback listener: %v", err)
	}
	defer func() { _ = ln.Close() }()
	opened := false
	o := OAuth{ClientID: "c", Addr: ln.Addr().String(), OpenBrowser: func(string) error { opened = true; return nil }}
	_, err = o.Authorize(t.Context())
	if !errors.Is(err, ErrPortBusy) || !strings.Contains(err.Error(), ln.Addr().String()) {
		t.Fatalf("error %v", err)
	}
	if opened {
		t.Fatal("the browser opened although the callback could not listen")
	}
}

func TestAuthorizeNeedsACallbackAddress(t *testing.T) {
	opened := false
	o := OAuth{ClientID: "c", OpenBrowser: func(string) error { opened = true; return nil }}
	if _, err := o.Authorize(t.Context()); err == nil || opened {
		t.Fatalf("no address: err %v, browser opened %t", err, opened)
	}
	if _, err := (OAuth{Addr: "127.0.0.1:0"}).Authorize(t.Context()); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("no client id: %v", err)
	}
}

func TestTheDefaultPagesAreNeutral(t *testing.T) {
	page := make(chan string, 1)
	o := OAuth{ClientID: "c", Timeout: time.Second, Addr: "127.0.0.1:0", OpenBrowser: func(raw string) error {
		u, _ := url.Parse(raw)
		go func() {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u.Query().Get("redirect_uri")+"?error=access_denied&state="+u.Query().Get("state"), nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				page <- "request failed: " + err.Error()
				return
			}
			b, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				page <- "read failed: " + err.Error()
				return
			}
			page <- string(b)
		}()
		return nil
	}}
	_, _ = o.Authorize(t.Context())
	if p := <-page; p != defaultFailText {
		t.Fatalf("page = %q", p)
	}
}

func TestFreshWithoutAGrantReportsNoTokens(t *testing.T) {
	o := OAuth{ClientID: "c", Store: &memStore{}}
	if _, err := o.Fresh(t.Context()); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("err = %v", err)
	}
}

func TestFreshMapsARefusedRefreshToSignedOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &memStore{}
	if err := store.Save(Tokens{Access: "A", Refresh: "R", Expires: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	o := OAuth{ClientID: "cid", AuthBase: srv.URL, Now: func() time.Time { return now }, Store: store}
	if _, err := o.Fresh(t.Context()); !errors.Is(err, ErrRevoked) {
		t.Fatalf("err = %v", err)
	}
}

func TestAStoredGrantWithNoAccessTokenRefreshes(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &memStore{t: Tokens{Refresh: "R", Expires: now.Add(time.Hour)}, has: true}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"access_token":"N","refresh_token":"R2","expires_in":3600}`))
	}))
	defer srv.Close()
	o := OAuth{ClientID: "cid", AuthBase: srv.URL, Now: func() time.Time { return now }, Store: store}
	got, err := o.Fresh(t.Context())
	if err != nil || got.Access != "N" || got.Refresh != "R2" || calls != 1 || store.t.Access != "N" {
		t.Fatalf("a stored refresh token with no access token must refresh: %+v, %v, %d calls", got, err, calls)
	}
}

func TestListenFailureThatIsNotABusyPortKeepsItsCause(t *testing.T) {
	o := OAuth{ClientID: "c", Addr: "256.0.0.1:0", OpenBrowser: func(string) error { return nil }}
	_, err := o.Authorize(t.Context())
	if err == nil || errors.Is(err, ErrPortBusy) || !strings.Contains(err.Error(), "256.0.0.1:0") {
		t.Fatalf("error %v", err)
	}
}

func TestExchangeKeepsAServerFailureApartFromARevokedGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer srv.Close()
	o := OAuth{ClientID: "cid", AuthBase: srv.URL}
	_, err := o.exchange(t.Context(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"R"}})
	var se *httpkit.StatusError
	if err == nil || errors.Is(err, ErrRevoked) || !errors.As(err, &se) || se.Code != http.StatusBadGateway {
		t.Fatalf("502 on refresh: %v", err)
	}
}

func TestExchangeDoesNotFollowARedirectWithTheRefreshToken(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		leaked = r.PostForm.Get("refresh_token")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	o := OAuth{ClientID: "cid", AuthBase: srv.URL}
	if _, err := o.exchange(t.Context(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"SECRET"}}); err == nil {
		t.Fatal("a redirected token exchange succeeded")
	}
	if leaked != "" {
		t.Fatalf("the refresh token reached another origin: %q", leaked)
	}
}

func TestExchangeRefusesAnExpiryThatOverflows(t *testing.T) {
	for _, in := range []string{"10000000000", "-5"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"A","refresh_token":"R","expires_in":` + in + `}`))
		}))
		o := OAuth{ClientID: "cid", AuthBase: srv.URL}
		_, err := o.exchange(t.Context(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"R"}})
		srv.Close()
		if err == nil {
			t.Fatalf("expires_in %s was accepted", in)
		}
	}
}

func TestAFailedSaveKeepsTheRotatedGrantForTheNextFresh(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &memStore{t: Tokens{Access: "old", Refresh: "R1", Expires: now.Add(-time.Hour)}, has: true, saveErr: errors.New("keyring locked")}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		if r.PostForm.Get("refresh_token") != "R1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"A2","refresh_token":"R2","expires_in":3600}`))
	}))
	defer srv.Close()
	o := OAuth{ClientID: "cid", AuthBase: srv.URL, Now: func() time.Time { return now }, Store: store}
	got, err := o.Fresh(t.Context())
	if err == nil || got.Refresh != "R2" {
		t.Fatalf("a failed save must surface with the new grant: %+v, %v", got, err)
	}
	store.saveErr = nil
	got, err = o.Fresh(t.Context())
	if err != nil || got.Refresh != "R2" || calls != 1 || store.t.Refresh != "R2" {
		t.Fatalf("the spent refresh token was sent again: %+v, %v, %d calls, stored %+v", got, err, calls, store.t)
	}
}

func TestCleartextEndpointsAreRefusedExceptOnLoopback(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://users.nexusmods.com/oauth": true, "http://127.0.0.1:8080/oauth": true, "http://localhost/x": true,
		"http://[::1]:1/x": true, "http://users.nexusmods.com/oauth": false, "ws://sso.nexusmods.com": false,
	} {
		scheme := "https"
		if err := requireTLS(raw, scheme); (err == nil) != ok {
			t.Errorf("%s: err = %v", raw, err)
		}
	}
	o := OAuth{ClientID: "c", AuthBase: "http://users.nexusmods.com/oauth"}
	if _, err := o.exchange(t.Context(), url.Values{"grant_type": {"refresh_token"}}); err == nil {
		t.Fatal("a cleartext token endpoint was used")
	}
	if _, err := o.Identify(t.Context(), "tok"); err == nil || !strings.Contains(err.Error(), "must be https://") {
		t.Fatalf("a cleartext userinfo endpoint was used: %v", err)
	}
}
