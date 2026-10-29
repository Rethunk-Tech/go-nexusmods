package nexusmods

import (
	"encoding/json"
	"net/http"
	"testing"
)

func decodeQuery(t testing.TB, r *http.Request) (q struct {
	Variables map[string]any `json:"variables"`
},
) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
		t.Fatal(err)
	}
	return q
}

// FuzzParseModURL holds that a link ParseModURL accepts names the same mod when rebuilt: it reads remote data
// (requirement URLs) and pasted text.
func FuzzParseModURL(f *testing.F) {
	f.Add("https://www.nexusmods.com/stardewvalley/mods/1915?tab=files")
	f.Add("https://next.nexusmods.com/games/stardewvalley/mods/1915/")
	f.Add("https://www.nexusmods.com/a/mods/0")
	f.Add("%zz")
	f.Fuzz(func(t *testing.T, text string) {
		domain, id, ok := ParseModURL(text)
		if !ok {
			return
		}
		d2, id2, ok2 := ParseModURL(ModURL(domain, id))
		if !ok2 || d2 != domain || id2 != id {
			t.Fatalf("%q read as %q %d, rebuilt as %q %d (%t)", text, domain, id, d2, id2, ok2)
		}
	})
}

// FuzzChangelogs holds that no body makes the changelog reader panic or return more than the limit; the version
// sort runs on remote strings.
func FuzzChangelogs(f *testing.F) {
	f.Add(fixture(f, "changelogs-541.json"), 3)
	f.Add([]byte(`{"3.5.2":["c"],"1.0":["a"],"not a version":["x"],"1.0":["dup"]}`), 2)
	f.Add([]byte(`[]`), 5)
	f.Add([]byte(`{"1":`), 1)
	f.Fuzz(func(t *testing.T, raw []byte, limit int) {
		got, err := parseChangelogs(raw, limit)
		if err == nil && len(got) > max(limit, 0) {
			t.Fatalf("%d entries for limit %d", len(got), limit)
		}
	})
}
