package nexusmods

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDomainCannotSteerThePath(t *testing.T) {
	var path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.EscapedPath())
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := New("T", "1")
	c.BaseURL = srv.URL
	_, _ = c.WithKey("k").Files(t.Context(), Title{Domain: "x/../../v3/uploads"}, 1)
	if p, _ := path.Load().(string); !strings.HasPrefix(p, "/v1/games/x%2F..%2F..%2Fv3%2Fuploads/") {
		t.Fatalf("path = %q", p)
	}
}

func TestEmptyBearerTokenIsRefused(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	c := New("T", "1")
	c.BaseURL = srv.URL
	_, err := c.WithBearer(func(context.Context) (string, error) { return "", nil }).Validate(t.Context())
	if !errors.Is(err, ErrUnauthorized) || hits.Load() != 0 {
		t.Fatalf("err=%v hits=%d", err, hits.Load())
	}
}

func TestStorageRedirectCannotLeaveHTTPS(t *testing.T) {
	var plain atomic.Int32
	cleartext := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { plain.Add(1) }))
	defer cleartext.Close()
	store := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, cleartext.URL, http.StatusTemporaryRedirect)
	}))
	defer store.Close()
	c := New("T", "1")
	c.HTTP = store.Client()
	_, err := NewUploader(c).send(t.Context(), http.MethodPut, store.URL, "application/octet-stream", strings.NewReader("data"), 4)
	if err == nil || plain.Load() != 0 {
		t.Fatalf("err=%v cleartext hits=%d", err, plain.Load())
	}
}
