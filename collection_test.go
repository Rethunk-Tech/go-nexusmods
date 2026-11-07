package nexusmods

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPremiumCollectionArchiveGivesUpOnAStalledMirror(t *testing.T) {
	old := mirrorTimeout
	mirrorTimeout = 100 * time.Millisecond
	t.Cleanup(func() { mirrorTimeout = old })
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mirror" {
			w.WriteHeader(http.StatusOK)
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"download_links":[{"URI":"` + srv.URL + `/mirror"}]}`))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "9.9")
	c.BaseURL, c.HTTP = srv.URL, srv.Client()
	c = c.WithKey("k")
	start := time.Now()
	if _, err := c.PremiumCollectionArchive(t.Context(), "/v2/collections/x/revisions/1/download_link"); err == nil {
		t.Fatal("stalled mirror succeeded")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("did not give up promptly")
	}
}

func TestPremiumCollectionArchiveAllowsASlowSteadyMirror(t *testing.T) {
	old := mirrorTimeout
	mirrorTimeout = 200 * time.Millisecond
	t.Cleanup(func() { mirrorTimeout = old })
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mirror" {
			for range 6 {
				_, _ = w.Write([]byte("x"))
				_ = http.NewResponseController(w).Flush()
				time.Sleep(100 * time.Millisecond)
			}
			return
		}
		_, _ = w.Write([]byte(`{"download_links":[{"URI":"` + srv.URL + `/mirror"}]}`))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "9.9")
	c.BaseURL, c.HTTP = srv.URL, srv.Client()
	c = c.WithKey("k")
	b, err := c.PremiumCollectionArchive(t.Context(), "/v2/collections/x/revisions/1/download_link")
	if err != nil || len(b) != 6 {
		t.Fatalf("got %d bytes, err %v", len(b), err)
	}
}

func TestPremiumCollectionArchiveErrorDropsTheSignedQuery(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"download_link":{"URI":"https://127.0.0.1:1/archive.zip?md5=SECRETMD5&expires=99"}}`))
	}))
	t.Cleanup(api.Close)
	c := New("TestApp", "9.9")
	c.BaseURL = api.URL
	_, err := c.WithKey("k").PremiumCollectionArchive(t.Context(), "/v2/collections/x/revisions/1/download_link")
	if err == nil || strings.Contains(err.Error(), "SECRETMD5") {
		t.Fatalf("err = %v", err)
	}
}

func TestPremiumCollectionArchiveRefusesAnHTTPSDowngradeRedirect(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) }))
	t.Cleanup(plain.Close)
	mirror := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	t.Cleanup(mirror.Close)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"download_link":{"URI":"` + mirror.URL + `/a"}}`))
	}))
	t.Cleanup(api.Close)
	c := New("TestApp", "9.9")
	c.BaseURL, c.HTTP = api.URL, mirror.Client()
	if _, err := c.WithKey("k").PremiumCollectionArchive(t.Context(), "/v2/collections/x/revisions/1/download_link"); err == nil ||
		!strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("err = %v", err)
	}
}
