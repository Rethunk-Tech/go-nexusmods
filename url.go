package nexusmods

import (
	"net/url"
	"strconv"
	"strings"
)

// ModURL is a mod's page on Nexus.
func ModURL(domain string, modID int) string {
	return "https://www.nexusmods.com/" + domain + "/mods/" + strconv.Itoa(modID)
}

// ParseModURL reads a Nexus mod page link, in the old (/<domain>/mods/<id>) or new (/games/<domain>/mods/<id>)
// form; tabs, queries and fragments are ignored.
func ParseModURL(text string) (domain string, modID int, ok bool) {
	u, err := url.Parse(strings.TrimSpace(text))
	if err != nil || u.Scheme != "https" {
		return "", 0, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	if host != "nexusmods.com" && host != "next.nexusmods.com" {
		return "", 0, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 1 && parts[0] == "games" {
		parts = parts[1:]
	}
	if len(parts) != 3 || !validDomain(parts[0]) || parts[1] != "mods" {
		return "", 0, false
	}
	id, err := strconv.Atoi(parts[2])
	if err != nil || id < 1 || strconv.Itoa(id) != parts[2] {
		return "", 0, false
	}
	return parts[0], id, true
}

// validDomain holds a game domain to the URL-safe set Nexus uses, so a percent-decoded byte cannot survive into ModURL.
func validDomain(domain string) bool {
	if domain == "" {
		return false
	}
	for _, r := range domain {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
