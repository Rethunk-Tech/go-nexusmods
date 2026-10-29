package nexusmods

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
)

func TestDownloadFormsSendWhatTheirNamesSay(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Query())
		_, _ = w.Write(fixture(t, "download-link.json"))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "9.9")
	c.BaseURL = srv.URL
	c = c.WithKey("k")
	ctx := t.Context()

	if _, err := c.PremiumDownloadLinks(ctx, stardew, 541, 3001); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DownloadLinks(ctx, stardew, NXMLink{ModID: 541, FileID: 3001, Key: "KEY", Expires: 1234}); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || len(queries[0]) != 0 || queries[1].Get("key") != "KEY" || queries[1].Get("expires") != "1234" {
		t.Fatalf("queries = %v", queries)
	}
	for _, l := range []NXMLink{{ModID: 541, FileID: 3001, Expires: 1234}, {ModID: 541, FileID: 3001, Key: "KEY"}, {Key: "KEY", Expires: -1}} {
		if _, err := c.DownloadLinks(ctx, stardew, l); err == nil {
			t.Fatalf("%+v was sent", l)
		}
	}
	if len(queries) != 2 {
		t.Fatalf("a refused link reached the server: %v", queries)
	}
}

func TestCredentialHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write(fixture(t, "validate.json"))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "9.9")
	c.BaseURL = srv.URL
	ctx := t.Context()

	if _, err := c.Validate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["Apikey"]; ok || got.Get("Authorization") != "" {
		t.Fatalf("an unauthenticated client sent a credential: %v", got)
	}
	if _, err := c.WithBearer(func(context.Context) (string, error) { return "tok", nil }).Validate(ctx); err != nil {
		t.Fatal(err)
	}
	if got.Get("Authorization") != "Bearer tok" || got.Get("Apikey") != "" {
		t.Fatalf("bearer headers = %v", got)
	}
	if _, err := c.WithBearer(func(context.Context) (string, error) { return "", errors.New("signed out") }).Validate(ctx); err == nil {
		t.Fatal("a failing token source was ignored")
	}
}

// A caller records reachability by putting an httpkit.Transport under the client; it must see every request, the
// archive mirror's included, and a transport error must reach it before net/http wraps the URL into it.
func TestEveryRoundTripReachesTheCallersTransport(t *testing.T) {
	var seen atomic.Int32
	var lastErr atomic.Value
	hc := &http.Client{Transport: &httpkit.Transport{OnResult: func(_ *http.Request, _ *http.Response, err error) {
		seen.Add(1)
		if err != nil {
			lastErr.Store(err)
		}
	}}}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mirror" {
			_, _ = w.Write([]byte("zip"))
			return
		}
		_, _ = w.Write([]byte(`{"download_link":{"URI":"` + strings.Replace(srv.URL, "http://", "https://", 1) + `/mirror"}}`))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "9.9")
	c.BaseURL, c.HTTP = srv.URL, hc
	c = c.WithKey("k")

	if _, err := c.Validate(t.Context()); err != nil || seen.Load() != 1 {
		t.Fatalf("validate: seen %d, %v", seen.Load(), err)
	}
	// The mirror address is https on a plain server, so the second request fails in transport: it is still observed.
	if _, err := c.PremiumCollectionArchive(t.Context(), "/v2/collections/x/revisions/1/download_link"); err == nil {
		t.Fatal("expected the https mirror on a plain server to fail")
	}
	if seen.Load() != 3 || lastErr.Load() == nil {
		t.Fatalf("seen %d round trips, last error %v", seen.Load(), lastErr.Load())
	}
}

// The mirror's stall cancels the request, and a body that then ends cleanly must still fail the fetch. The fake
// transport ends the body only after the cancel, so the race that a real socket decides by timing is fixed here.
func TestPremiumCollectionArchiveFailsWhenAStalledMirrorEndsCleanly(t *testing.T) {
	old := mirrorTimeout
	mirrorTimeout = 50 * time.Millisecond
	t.Cleanup(func() { mirrorTimeout = old })
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"download_link":{"URI":"https://mirror.test/archive"}}`))
	}))
	t.Cleanup(api.Close)
	c := New("TestApp", "9.9")
	c.BaseURL, c.HTTP = api.URL, &http.Client{Transport: mirrorStub{}}
	c = c.WithKey("k")

	_, err := c.PremiumCollectionArchive(t.Context(), "/v2/collections/x/revisions/1/download_link")
	if !errors.Is(err, errMirrorStalled) {
		t.Fatalf("a stalled mirror that ended its body cleanly gave %v", err)
	}
}

// mirrorStub sends the API to the real test server and answers the mirror with a body that blocks until the
// request is cancelled, then reports a clean end.
type mirrorStub struct{}

func (mirrorStub) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "mirror.test" {
		return http.DefaultTransport.RoundTrip(req)
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{}, Body: cleanAfterCancel{done: req.Context().Done()}}, nil
}

type cleanAfterCancel struct{ done <-chan struct{} }

func (b cleanAfterCancel) Read([]byte) (int, error) {
	<-b.done
	return 0, io.EOF
}

func (cleanAfterCancel) Close() error { return nil }

func TestPremiumCollectionArchiveRefusesOtherPathsAndSchemes(t *testing.T) {
	var hits atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"download_link":{"URI":"http://mirror.test/archive"}}`))
	}))
	t.Cleanup(api.Close)
	c := New("TestApp", "9.9")
	c.BaseURL = api.URL
	c = c.WithKey("k")
	for _, link := range []string{"/v1/users/validate.json", "/v2/collections/../graphql", "https://evil.test/v2/collections/x"} {
		if _, err := c.PremiumCollectionArchive(t.Context(), link); err == nil {
			t.Fatalf("%q was fetched", link)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("a refused link reached the API")
	}
	if _, err := c.PremiumCollectionArchive(t.Context(), "/v2/collections/x/revisions/1/download_link"); err == nil {
		t.Fatal("a plain-http mirror was accepted")
	}
}

func TestNewerFileAndNXMDomain(t *testing.T) {
	for _, tc := range []struct {
		a       string
		aID     int64
		b       string
		bID     int64
		aNewest bool
	}{
		{"2.0.0", 1, "1.9.0", 9, true},
		{"1.0.0", 9, "1.0.0", 1, true},
		{"1.0.0", 1, "1.0.0", 9, false},
		{"not a version", 5, "1.0", 1, true},
	} {
		if got := NewerFile(tc.a, tc.aID, tc.b, tc.bID); got != tc.aNewest {
			t.Errorf("NewerFile(%q, %d, %q, %d) = %t", tc.a, tc.aID, tc.b, tc.bID, got)
		}
	}
	if d, err := NXMDomain("nxm://stardewvalley/mods/1/files/2?key=k"); err != nil || d != "stardewvalley" {
		t.Fatalf("domain = %q, %v", d, err)
	}
	if _, err := NXMDomain("https://stardewvalley/"); err == nil {
		t.Fatal("a non-nxm link has a domain")
	}
}
