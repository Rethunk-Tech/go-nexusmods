package nexusmods

import (
	"net/http"
	"testing"
	"time"
)

// FuzzSearch holds that a GraphQL search body never panics and stamps Updated in UTC when it is present.
func FuzzSearch(f *testing.F) {
	f.Add([]byte(`{"data":{"mods":{"totalCount":7,"nodes":[{"modId":3,"name":"A","author":"Ann","downloads":9,"updatedAt":"2026-01-02T03:04:05Z","fileSize":2,"modCategory":{"name":"Audio"}}]}}}`))
	f.Add([]byte(`{"data":{"mods":{"totalCount":0,"nodes":[]}}}`))
	f.Add([]byte(`{"errors":[{"message":"x"}]}`))
	f.Add([]byte(`{"data":{"mods":{"nodes":[{"updatedAt":"2026-01-02T03:04:05+02:00"}]}}}`))
	f.Add([]byte(`[`))
	f.Fuzz(func(t *testing.T, body []byte) {
		c := New("TestApp", "1.0")
		c.HTTP, c.BaseURL = &http.Client{Transport: canned(body)}, "http://nexus.test"
		c = c.WithKey("k")
		res, err := c.Search(t.Context(), SearchQuery{Domain: "stardew", Text: "a"})
		if err != nil {
			return
		}
		for _, m := range res.Mods {
			if !m.Updated.IsZero() && m.Updated.Location() != time.UTC {
				t.Fatalf("updated not UTC: %v", m.Updated)
			}
		}
	})
}
