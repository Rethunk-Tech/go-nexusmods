package nexusmods

import (
	"errors"
	"testing"
	"time"
)

func FuzzParseNXM(f *testing.F) {
	f.Add("nxm://stardewvalley/mods/1/files/2?key=k&expires=9999999999&user_id=7")
	f.Add("nxm://x/mods/-1/files/2?key=&expires=1&user_id=1")
	f.Add("nxm://a:80/mods/1/files/2#f")
	f.Add("%zz")
	gameOf := func(string) (string, bool) { return "g", true }
	now := time.Unix(1000, 0)
	f.Fuzz(func(t *testing.T, raw string) {
		l, err := ParseNXM(raw, 7, now, gameOf)
		if err == nil {
			if l.Key == "" || l.UserID != 7 || l.Expires <= now.Unix() {
				t.Fatalf("accepted bad link %q: %+v", raw, l)
			}
			return
		}
		if _, ok := errors.AsType[*RejectError](err); !ok {
			t.Fatalf("untyped error for %q: %v", raw, err)
		}
	})
}

func FuzzNXMDomain(f *testing.F) {
	f.Add("nxm://stardewvalley/mods/1915/files/77?key=abc_-x&expires=1000600&user_id=42")
	f.Add("nxm://skyrim/mods/1/files/2?key=k&expires=1000600&user_id=42")
	f.Add("nxm://oauth/callback?code=1")
	f.Add("nxm://me@stardewvalley/mods/1/files/2")
	f.Add("nxm://")
	f.Add("%zz")
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) {
		host, err := NXMDomain(raw)
		if err == nil {
			if host == "" {
				t.Fatalf("accepted empty host for %q", raw)
			}
			return
		}
		if host != "" {
			t.Fatalf("error with host %q for %q: %v", host, raw, err)
		}
		if _, ok := errors.AsType[*RejectError](err); !ok {
			t.Fatalf("untyped error for %q: %v", raw, err)
		}
	})
}
