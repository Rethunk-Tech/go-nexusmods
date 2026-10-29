package nexusmods

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// The JSON of these types is a stored and printed format, so each is compared byte for byte with a fixture and
// decoded back: a renamed tag changes what a caller's cache or script reads.
func TestJSONFormsAreStable(t *testing.T) {
	at := time.Date(2026, 3, 15, 2, 54, 41, 0, time.UTC)
	for name, v := range map[string]any{
		"golden-page.json": Page{
			ModID: 541, Name: "Lookup Anything", Summary: "s", Description: "d", PictureURL: "https://example.test/p.png",
			Version: "1.55.0", Author: "a", UploadedBy: "u", UploaderURL: "https://example.test/u", CategoryID: 10,
			Endorsements: 3, Downloads: 4, UniqueDownloads: 5, Created: at, Updated: at, Adult: true,
			Status: "published", Available: true, Endorsement: "Endorsed",
			Requirements: []Requirement{{ModID: 1915, Name: "Content Patcher", URL: "https://www.nexusmods.com/stardewvalley/mods/1915", Notes: "n"}},
		},
		"golden-file.json": File{
			FileID: 3001, FileName: "f.zip", Name: "F", Description: "d", Version: "2.0.0", ModVersion: "2.0.0", Category: "MAIN",
			SizeKB: 2048, IsPrimary: true, Uploaded: at, MD5: "abc", ReplacedBy: 3002,
		},
		"golden-trackedmod.json": TrackedMod{ModID: 541, DomainName: "stardewvalley"},
		"golden-limits.json": Limits{
			Known:  true,
			Daily:  Window{Remaining: 19000, Limit: 20000, Reset: at},
			Hourly: Window{Remaining: 1900, Limit: 2000, Reset: at},
		},
		"golden-nxmlink.json": NXMLink{Game: "g", ModID: 1, FileID: 2, Key: "k", Expires: 3, UserID: 4},
		"golden-collection.json": Collection{
			Name: "Cozy Farm", Author: "Pat", Slug: "cozy-farm", Revision: 4, Files: []CollectionFile{{ModID: 100, FileID: 11, Optional: true}},
			Instructions: "i", External: []CollectionExternal{{Name: "Tool", Type: "browse", URL: "https://example.test", Version: "1", Author: "a", Optional: true}},
			DownloadLink: "/v2/collections/1/revisions/4/download_link",
		},
	} {
		got, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if want := bytes.TrimSpace(fixture(t, name)); !bytes.Equal(got, want) {
			t.Errorf("%s:\n got %s\nwant %s", name, got, want)
		}
		back := reflect.New(reflect.TypeOf(v))
		if err := json.Unmarshal(got, back.Interface()); err != nil || !reflect.DeepEqual(back.Elem().Interface(), v) {
			t.Errorf("%s did not decode back: %v", name, err)
		}
	}
}
