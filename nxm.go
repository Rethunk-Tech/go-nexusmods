package nexusmods

import (
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NXMLink is an accepted nxm:// download link, the value Nexus's Mod Manager Download button produces.
type NXMLink struct {
	// Game is the caller's id for the game the link's Nexus domain belongs to.
	Game    string `json:"game"`
	ModID   int    `json:"modId"`
	FileID  int    `json:"fileId"`
	Key     string `json:"key"`
	Expires int64  `json:"expires"`
	UserID  int    `json:"userId"`
}

// Why a link was refused. They are stable strings a UI can translate.
const (
	ReasonForm     = "form"
	ReasonGame     = "game"
	ReasonExpired  = "expired"
	ReasonUser     = "user"
	ReasonSignedIn = "signedIn"
)

// RejectError says why a link was refused.
type RejectError struct{ Reason string }

func (e *RejectError) Error() string { return "nxm link refused: " + e.Reason }

func reject(reason string) error { return &RejectError{Reason: reason} }

// GameOf maps an nxm link's Nexus domain to the caller's game id; ok is false for a game the caller does not serve.
type GameOf func(domain string) (gameID string, ok bool)

// ParseNXM accepts nxm://<domain>/mods/<mod id>/files/<file id>?key=&expires=&user_id= only for a domain gameOf maps to
// a game: numeric ids, a key, not yet expired at now, and user_id equal to userID, the signed-in account. Anything
// else is a *RejectError.
func ParseNXM(raw string, userID int, now time.Time, gameOf GameOf) (NXMLink, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "nxm" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return NXMLink{}, reject(ReasonForm)
	}
	gameID, ok := gameOf(u.Host)
	if !ok {
		return NXMLink{}, reject(ReasonGame)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "mods" || parts[2] != "files" {
		return NXMLink{}, reject(ReasonForm)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 3 {
		return NXMLink{}, reject(ReasonForm)
	}
	l := NXMLink{Game: gameID}
	if l.ModID, err = number(parts[1]); err != nil {
		return NXMLink{}, err
	}
	if l.FileID, err = number(parts[3]); err != nil {
		return NXMLink{}, err
	}
	expires, err := number(q.Get("expires"))
	if err != nil {
		return NXMLink{}, err
	}
	if l.UserID, err = number(q.Get("user_id")); err != nil {
		return NXMLink{}, err
	}
	l.Expires, l.Key = int64(expires), q.Get("key")
	if l.Key == "" || len(q["key"]) != 1 {
		return NXMLink{}, reject(ReasonForm)
	}
	if userID == 0 {
		return NXMLink{}, reject(ReasonSignedIn)
	}
	if l.UserID != userID {
		return NXMLink{}, reject(ReasonUser)
	}
	if l.Expires <= now.Unix() {
		return NXMLink{}, reject(ReasonExpired)
	}
	return l, nil
}

// number reads a positive decimal integer, no sign, spaces or leading junk.
func number(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || strconv.Itoa(n) != s {
		return 0, reject(ReasonForm)
	}
	return n, nil
}

// NXMDomain is the nxm URL's game segment (the host), or a *RejectError when raw is not an nxm link.
func NXMDomain(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "nxm" || u.Host == "" {
		return "", reject(ReasonForm)
	}
	return u.Host, nil
}
