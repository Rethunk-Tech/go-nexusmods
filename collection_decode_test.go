package nexusmods

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/go-httpkit"
)

func TestDecodeCollection(t *testing.T) {
	body := []byte(`{"data":{"collectionRevision":{"revisionNumber":4,"collection":{"name":"Cozy Farm","slug":"cozy-farm","user":{"name":"Pat"}},"modFiles":[{"fileId":11,"optional":false,"file":{"modId":100,"name":"A","version":"1.0","mod":{"name":"A"}}},{"fileId":22,"optional":true,"file":{"modId":200}},{"fileId":33,"optional":false,"file":null}]}}}`)
	got, err := decodeCollection("cozy-farm", body)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Cozy Farm" || got.Author != "Pat" || got.Slug != "cozy-farm" || got.Revision != 4 {
		t.Fatalf("collection = %+v", got)
	}
	if len(got.Files) != 2 || got.Files[0] != (CollectionFile{ModID: 100, FileID: 11}) || got.Files[1] != (CollectionFile{ModID: 200, FileID: 22, Optional: true}) {
		t.Fatalf("files = %+v", got.Files)
	}

	_, err = decodeCollection("missing", []byte(`{"data":{"collectionRevision":null}}`))
	if !errors.Is(err, httpkit.ErrNotFound) || !strings.Contains(err.Error(), `nexus has no collection "missing"`) {
		t.Fatalf("null revision: %v", err)
	}
	_, err = decodeCollection("gone", []byte(`{"errors":[{"message":"not found"}]}`))
	if !errors.Is(err, httpkit.ErrNotFound) {
		t.Fatalf("errors: %v", err)
	}
}

func TestDecodeCollectionRevisionFixture(t *testing.T) {
	got, err := decodeCollection("htknoa", fixture(t, "collection-revision.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Instructions != "Start a **new save**. See https://example.com/guide" || got.DownloadLink != "/v2/collections/636/revisions/948/download_link" {
		t.Fatalf("collection = %+v", got)
	}
	want := CollectionExternal{Name: "Hand Mod", Type: "browse", URL: "https://example.com/mod", Version: "1.2", Author: "Sam", Optional: true}
	if len(got.External) != 1 || got.External[0] != want || len(got.Files) != 3 {
		t.Fatalf("external = %+v files = %d", got.External, len(got.Files))
	}
}

func TestCollectionDropsNonWebLinks(t *testing.T) {
	body := []byte(`{"data":{"collectionRevision":{"externalResources":[{"name":"a","resourceUrl":"ms-settings:"},{"name":"b","resourceUrl":"file:///etc/passwd"},{"name":"c","resourceUrl":"https:///nohost"},{"name":"d","resourceUrl":"http://example.test/x"}]}}}`)
	got, err := decodeCollection("x", body)
	if err != nil || len(got.External) != 4 {
		t.Fatalf("%+v, %v", got, err)
	}
	for i, want := range []string{"", "", "", "http://example.test/x"} {
		if got.External[i].URL != want {
			t.Errorf("external %d URL = %q, want %q", i, got.External[i].URL, want)
		}
	}
}

func TestCollectionReadsOverGraphQL(t *testing.T) {
	var vars map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/graphql" || r.Header.Get("Application-Name") != "TestApp" || r.Header.Get("Apikey") != "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		vars = decodeQuery(t, r).Variables
		_, _ = w.Write(fixture(t, "collection-revision.json"))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "9.9")
	c.BaseURL = srv.URL

	got, err := c.Collection(t.Context(), "stardewvalley", "htknoa", 0)
	if err != nil || got.DownloadLink == "" {
		t.Fatalf("%+v, %v", got, err)
	}
	if _, has := vars["revision"]; has || vars["slug"] != "htknoa" || vars["domain"] != "stardewvalley" {
		t.Fatalf("latest revision variables = %v", vars)
	}
	if _, err := c.Collection(t.Context(), "stardewvalley", "htknoa", 7); err != nil || vars["revision"] != float64(7) {
		t.Fatalf("revision variables = %v, %v", vars, err)
	}
}

// FuzzDecodeCollection feeds Nexus's answer for a collection revision: its mod list, curator instructions and the
// resources to install by hand.
func FuzzDecodeCollection(f *testing.F) {
	f.Add([]byte(`{"data":{"collectionRevision":{"revisionNumber":4,"downloadLink":"/v2/collections/1/revisions/4/download_link","installationInfo":" Read me ",` +
		`"externalResources":[{"name":"Tool","resourceType":"ExternalSite","resourceUrl":"https://example.com/t","optional":true},{"name":"Bad","resourceUrl":"ms-settings:"}],` +
		`"collection":{"name":"Cozy Farm","slug":"cozy-farm","user":{"name":"Pat"}},"modFiles":[{"fileId":11,"optional":false,"file":{"modId":100}},{"fileId":33,"file":null}]}}}`))
	f.Add([]byte(`{"errors":[{"message":"not found"}]}`))
	f.Add([]byte(`{"data":{"collectionRevision":null}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		c, err := decodeCollection("slug", raw)
		if err != nil {
			return
		}
		for _, e := range c.External {
			if e.URL == "" {
				continue
			}
			if u, err := url.Parse(e.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				t.Fatalf("kept resource link %q", e.URL)
			}
		}
	})
}
