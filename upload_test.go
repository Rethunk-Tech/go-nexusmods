package nexusmods

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/Rethunk-Tech/go-safefs"
)

func writePackage(t *testing.T, body string) string {
	t.Helper()
	pkg := filepath.Join(t.TempDir(), "Ab-ShopDrop-1.2.0.zip")
	if err := safefs.WriteFile(pkg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return pkg
}

// fakeNexus is a Nexus v3 API and its storage. It records each request as "METHOD path" with its body, and answers the
// upload as "created" once before "available".
type fakeNexus struct {
	srv   *httptest.Server
	mu    sync.Mutex
	log   []string
	bodys map[string]string
	polls int
	// failChangelog answers the changelog request with a 500.
	failChangelog bool
}

func newFakeNexus(t *testing.T, parts, partSize int) *fakeNexus {
	t.Helper()
	f := &fakeNexus{bodys: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		defer f.mu.Unlock()
		f.log = append(f.log, key)
		f.bodys[key] = string(body)
		isAPI := strings.HasPrefix(r.URL.Path, "/v3/")
		if isAPI && (r.Header.Get("Apikey") != "k" || r.Header.Get("Application-Name") != "App" || r.Header.Get("Application-Version") != "9.9.9") {
			t.Errorf("%s headers = %v", key, r.Header)
		}
		if !isAPI && r.Header.Get("Apikey") != "" {
			t.Errorf("%s leaked the API key to storage", key)
		}
		switch {
		case key == "POST /v3/uploads":
			_, _ = fmt.Fprintf(w, `{"data":{"id":"up1","state":"created","presigned_url":"%s/s3/whole"}}`, f.srv.URL)
		case key == "POST /v3/uploads/multipart":
			urls := make([]string, parts)
			for i := range urls {
				urls[i] = fmt.Sprintf("%s/s3/part%d", f.srv.URL, i+1)
			}
			raw, _ := json.Marshal(urls)
			_, _ = fmt.Fprintf(w, `{"data":{"id":"up1","state":"created","part_size_bytes":%d,"part_presigned_urls":%s,"complete_presigned_url":"%s/s3/complete"}}`, partSize, raw, f.srv.URL)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/s3/"):
			w.Header().Set("ETag", `"etag-`+strings.TrimPrefix(r.URL.Path, "/s3/")+`"`)
		case key == "POST /s3/complete":
		case key == "POST /v3/uploads/up1/finalise":
			_, _ = w.Write([]byte(`{"data":{"id":"up1","state":"created"}}`))
		case key == "GET /v3/uploads/up1":
			f.polls++
			state := "created"
			if f.polls > 1 {
				state = "available"
			}
			_, _ = fmt.Fprintf(w, `{"data":{"id":"up1","state":"%s"}}`, state)
		case key == "POST /v3/mods/55/changelogs" && f.failChangelog:
			w.WriteHeader(http.StatusInternalServerError)
		case key == "POST /v3/mod-files/77/versions" || key == "POST /v3/mods/55/changelogs":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeNexus) uploader(singleMax int64) *Uploader {
	c := New("App", "9.9.9").WithKey("k")
	c.HTTP, c.BaseURL = f.srv.Client(), f.srv.URL
	return &Uploader{c: c, PollInterval: time.Millisecond, singleMax: singleMax}
}

func TestUploadSinglePutThenVersionAndChangelog(t *testing.T) {
	f := newFakeNexus(t, 0, 0)
	err := f.uploader(0).Upload(t.Context(), Release{Path: writePackage(t, "0123456789"), ModFileID: "77", Name: "ShopDrop", Version: "1.2.0", ModID: "55", Changelog: "- fixed it"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /v3/uploads", "PUT /s3/whole", "POST /v3/uploads/up1/finalise", "GET /v3/uploads/up1", "GET /v3/uploads/up1",
		"POST /v3/mod-files/77/versions", "POST /v3/mods/55/changelogs",
	}
	if !slices.Equal(f.log, want) {
		t.Fatalf("requests = %q", f.log)
	}
	if f.bodys["PUT /s3/whole"] != "0123456789" {
		t.Errorf("stored bytes = %q", f.bodys["PUT /s3/whole"])
	}
	for key, want := range map[string]string{
		"POST /v3/uploads":               `{"filename":"Ab-ShopDrop-1.2.0.zip","size_bytes":10}`,
		"POST /v3/mod-files/77/versions": `{"archive_existing_file":true,"file_category":"main","name":"ShopDrop","upload_id":"up1","version":"1.2.0"}`,
		"POST /v3/mods/55/changelogs":    `{"changelog":"- fixed it","version":"1.2.0"}`,
	} {
		if f.bodys[key] != want {
			t.Errorf("%s body = %s", key, f.bodys[key])
		}
	}
}

func TestUploadMultipartCompletesWithEachETag(t *testing.T) {
	f := newFakeNexus(t, 3, 4)
	// No mod id: no changelog request.
	if err := f.uploader(5).Upload(t.Context(), Release{Path: writePackage(t, "0123456789"), ModFileID: "77", Name: "ShopDrop", Version: "1.2.0", Changelog: "- fixed it"}); err != nil {
		t.Fatal(err)
	}
	for part, want := range map[string]string{"part1": "0123", "part2": "4567", "part3": "89"} {
		if f.bodys["PUT /s3/"+part] != want {
			t.Errorf("%s = %q", part, f.bodys["PUT /s3/"+part])
		}
	}
	wantXML := "<CompleteMultipartUpload>\n" +
		"  <Part>\n    <PartNumber>1</PartNumber>\n    <ETag>etag-part1</ETag>\n  </Part>\n" +
		"  <Part>\n    <PartNumber>2</PartNumber>\n    <ETag>etag-part2</ETag>\n  </Part>\n" +
		"  <Part>\n    <PartNumber>3</PartNumber>\n    <ETag>etag-part3</ETag>\n  </Part>\n</CompleteMultipartUpload>"
	if f.bodys["POST /s3/complete"] != wantXML {
		t.Errorf("complete = %q", f.bodys["POST /s3/complete"])
	}
	if slices.Contains(f.log, "POST /v3/mods/55/changelogs") || f.log[0] != "POST /v3/uploads/multipart" {
		t.Errorf("requests = %q", f.log)
	}
}

func TestUploadRefusesPartsThatMissTheFile(t *testing.T) {
	f := newFakeNexus(t, 2, 4)
	err := f.uploader(5).Upload(t.Context(), Release{Path: writePackage(t, "0123456789"), ModFileID: "77", Name: "ShopDrop", Version: "1.2.0"})
	if err == nil || slices.Contains(f.log, "PUT /s3/part1") {
		t.Errorf("err = %v, requests = %q", err, f.log)
	}
}

func TestUploadErrorsAreTyped(t *testing.T) {
	for _, c := range []struct {
		status int
		check  func(error) bool
	}{
		{http.StatusUnauthorized, func(err error) bool { return errors.Is(err, ErrUnauthorized) }},
		{http.StatusForbidden, func(err error) bool {
			se, ok := errors.AsType[*httpkit.StatusError](err)
			return ok && se.Code == http.StatusForbidden
		}},
		{http.StatusTooManyRequests, func(err error) bool { return errors.Is(err, httpkit.ErrRateLimited) }},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(c.status) }))
		cl := New("App", "9.9.9").WithKey("k")
		cl.HTTP, cl.BaseURL = srv.Client(), srv.URL
		err := NewUploader(cl).Upload(t.Context(), Release{Path: writePackage(t, "x"), ModFileID: "1", Name: "n", Version: "1.0.0"})
		if !c.check(err) {
			t.Errorf("%d: err = %v", c.status, err)
		}
		srv.Close()
	}
}

func TestStorageURLRefusesCleartextAndForeignHosts(t *testing.T) {
	for raw, base := range map[string]string{
		"http://bucket.example/part": "https://api.nexusmods.com",
		"http://other.example/part":  "http://127.0.0.1:1",
		"/relative":                  "https://api.nexusmods.com",
	} {
		if _, err := storageURL(raw, base); err == nil {
			t.Errorf("%s followed under %s", raw, base)
		}
	}
	if _, err := storageURL("https://bucket.example/part", "https://api.nexusmods.com"); err != nil {
		t.Error(err)
	}
}

func TestUploadRefusesPartCountsThatDoNotMatch(t *testing.T) {
	for name, c := range map[string]struct{ parts, partSize int }{
		"too many":      {4, 4},
		"zero size":     {3, 0},
		"overflow size": {3, 1 << 62},
	} {
		f := newFakeNexus(t, c.parts, c.partSize)
		err := f.uploader(5).Upload(t.Context(), Release{Path: writePackage(t, "0123456789"), ModFileID: "77", Name: "n", Version: "1.0.0"})
		if err == nil || slices.Contains(f.log, "PUT /s3/part1") {
			t.Errorf("%s: err = %v, requests = %q", name, err, f.log)
		}
	}
}

func TestStorageFailuresAreSafeAndTyped(t *testing.T) {
	var mode atomic.Value
	mode.Store("")
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/uploads":
			_, _ = fmt.Fprintf(w, `{"data":{"id":"up1","presigned_url":"%s/s3/whole?sig=SECRET"}}`, srv.URL)
		case "/s3/whole":
			if mode.Load() == "429" {
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			if h, ok := w.(http.Hijacker); ok {
				if conn, _, err := h.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
		}
	}))
	defer srv.Close()
	up := func() error {
		c := New("App", "9.9.9").WithKey("k")
		c.HTTP, c.BaseURL = srv.Client(), srv.URL
		return NewUploader(c).Upload(t.Context(), Release{Path: writePackage(t, "x"), ModFileID: "1", Name: "n", Version: "1.0.0"})
	}
	if err := up(); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("transport error = %v", err)
	}
	mode.Store("429")
	rl, ok := errors.AsType[*httpkit.RateLimitError](up())
	if !ok || rl.Reset.IsZero() {
		t.Errorf("429 = %v, %v", rl, ok)
	}
}

func TestUploadChangelogFailureIsTyped(t *testing.T) {
	f := newFakeNexus(t, 0, 0)
	f.failChangelog = true
	err := f.uploader(0).Upload(t.Context(), Release{Path: writePackage(t, "x"), ModFileID: "77", Name: "n", Version: "1.0.0", ModID: "55", Changelog: "c"})
	ce, ok := errors.AsType[*ChangelogError](err)
	if !ok || !strings.HasPrefix(err.Error(), "the file is up, but its changelog was not added: ") || errors.Unwrap(ce) == nil {
		t.Errorf("err = %v", err)
	}
	if _, ok := errors.AsType[*httpkit.StatusError](err); !ok {
		t.Errorf("cause lost: %v", err)
	}
}
