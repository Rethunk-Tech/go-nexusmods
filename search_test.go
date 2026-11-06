package nexusmods

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func searchClient(t *testing.T, reply string, status int, got *string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		*got = in.Query
		if r.Header.Get("Application-Name") != "TestApp" || r.URL.Path != "/v2/graphql" {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	c := New("TestApp", "test").WithKey("k")
	c.BaseURL = srv.URL
	return c
}

func TestSearchBuildsQueryAndDecodes(t *testing.T) {
	var query string
	c := searchClient(t, `{"data":{"mods":{"totalCount":7,"nodes":[{"modId":3,"name":"A","author":"Ann","downloads":9,"updatedAt":"2026-01-02T03:04:05Z","fileSize":2,"modCategory":{"name":"Audio"}}]}}}`, 200, &query)
	res, err := c.Search(t.Context(), SearchQuery{
		Domain: "stardew", Text: `a"b`, Sort: SortDownloads, Categories: []string{"Audio", "UI"},
		ExcludeCategories: []string{"Misc"}, Count: 500, Offset: 160,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`op:OR, filter:[{gameDomainName:[{value:"stardew"}], name:[{value:"a\"b", op:WILDCARD}], categoryName:[{value:"Audio"}, {value:"Misc", op:NOT_EQUALS}]}, {`,
		`{value:"UI"}`, `downloads:{direction:DESC}`, `count: 80, offset: 160`,
	} {
		if !strings.Contains(query, want) {
			t.Errorf("query lacks %s:\n%s", want, query)
		}
	}
	if res.Total != 7 || len(res.Mods) != 1 {
		t.Fatalf("result = %+v", res)
	}
	m := res.Mods[0]
	if m.ModID != 3 || m.Category != "Audio" || m.SizeKB != 2 || m.Updated.Year() != 2026 || m.Author != "Ann" {
		t.Errorf("mod = %+v", m)
	}
}

func TestSearchPlainFilterAndErrors(t *testing.T) {
	var query string
	c := searchClient(t, `{"data":{"mods":{"totalCount":0,"nodes":[]}}}`, 200, &query)
	if _, err := c.Search(t.Context(), SearchQuery{Domain: "d", Text: "  "}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, `mods(filter:{gameDomainName:[{value:"d"}]}, sort:[{endorsements:{direction:DESC}}], count: 80`) {
		t.Errorf("query = %s", query)
	}
	if _, err := c.Search(t.Context(), SearchQuery{}); err == nil {
		t.Error("empty domain accepted")
	}
	c = searchClient(t, ``, http.StatusUnauthorized, &query)
	if _, err := c.Search(t.Context(), SearchQuery{Domain: "d"}); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v", err)
	}
	c = searchClient(t, `{"errors":[{"message":"too complex"}]}`, 200, &query)
	if _, err := c.Search(t.Context(), SearchQuery{Domain: "d"}); err == nil || !strings.Contains(err.Error(), "too complex") {
		t.Errorf("err = %v", err)
	}
}
