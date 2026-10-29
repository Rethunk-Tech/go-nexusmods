package nexusmods

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/Rethunk-Tech/go-safefs"
)

// scanKey is one mod of one game, as mod ids repeat across games.
type scanKey struct{ game, mod int }

// Title names a game to Nexus: its domain in v1 URLs and its numeric id in the v2 API.
type Title struct {
	Domain string
	ID     int
}

const (
	// BaseURL is the public API.
	BaseURL = "https://api.nexusmods.com"
	// LimitFloor is the remaining-call count at or under which requests are refused until the window resets.
	LimitFloor = 5

	requestTimeout = 20 * time.Second
	maxBody        = 16 << 20
)

var (
	// ErrUnauthorized means the API key was rejected.
	ErrUnauthorized = errors.New("nexus rejected the API key")
	// ErrPremiumRequired means a download link needs the key and expires from an nxm:// link, or a premium account.
	ErrPremiumRequired = errors.New("nexus requires a premium account or an nxm:// link for this download")
)

// rateLimitError is a refused or rejected call; reset is when the exhausted window renews, zero when unknown. It is
// an *httpkit.RateLimitError, so errors.As and errors.Is(err, httpkit.ErrRateLimited) both match.
func rateLimitError(reset time.Time) error {
	return fmt.Errorf("nexus: %w", &httpkit.RateLimitError{Reset: reset})
}

// statusError is any other non-200 answer, named for Nexus in the message.
func statusError(code int, status string) error {
	return fmt.Errorf("nexus: %w", &httpkit.StatusError{Code: code, Status: status})
}

// Window is one rate-limit window as last reported by the API.
type Window struct {
	Remaining int       `json:"remaining"`
	Limit     int       `json:"limit"`
	Reset     time.Time `json:"reset"`
}

// Limits is the daily and hourly budget; Known is false until a response has carried the headers.
type Limits struct {
	Known  bool   `json:"known"`
	Daily  Window `json:"daily"`
	Hourly Window `json:"hourly"`
}

type limiter struct {
	mu sync.Mutex
	v  Limits
}

// Client talks to the API with one user's key or token. Copies made by WithKey and WithBearer share the rate-limit
// state and the caches.
type Client struct {
	// HTTP sends every request; nil means http.DefaultClient. Its redirect policy is chained with
	// httpkit.SameOrigin, so the credential never leaves the API origin. Give it an httpkit.Transport to set the
	// User-Agent and to observe every round trip through OnResult.
	HTTP *http.Client
	// BaseURL is the API origin; empty means BaseURL.
	BaseURL string
	// Now is the clock; nil means time.Now in UTC.
	Now      func() time.Time
	key      string
	bearer   TokenSource
	name     string
	version  string
	lim      *limiter
	track    *trackedCache
	scanMu   *sync.Mutex
	scans    map[scanKey]map[int]string
	onLimits func(Limits)
}

// New returns a client that sends Application-Name: name and Application-Version: version on every request, as Nexus
// asks of every app. It has no credential until WithKey or WithBearer.
func New(name, version string) *Client {
	return &Client{name: name, version: version, lim: &limiter{}, track: &trackedCache{}, scanMu: &sync.Mutex{}, scans: map[scanKey]map[int]string{}}
}

// with returns a client that authenticates with key or bearer and shares c's rate-limit and tracked-list state.
func (c *Client) with(key string, bearer TokenSource) *Client {
	return &Client{HTTP: c.HTTP, BaseURL: c.BaseURL, Now: c.Now, key: key, bearer: bearer, name: c.name, version: c.version, lim: c.lim, track: c.track, scanMu: c.scanMu, scans: c.scans, onLimits: c.onLimits}
}

// SetLimitsHook sets fn, called after a response updates the rate-limit budget and with no lock held, so it may
// call Limits. Set it before sharing the client between goroutines.
func (c *Client) SetLimitsHook(fn func(Limits)) {
	c.onLimits = fn
}

// Limits returns the budget from the latest response.
func (c *Client) Limits() Limits {
	c.lim.mu.Lock()
	defer c.lim.mu.Unlock()
	return c.lim.v
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now().UTC()
}

// blocked returns the error to refuse with when a window is at the floor and has not reset yet.
func (c *Client) blocked() error {
	l := c.Limits()
	if !l.Known {
		return nil
	}
	now := c.now()
	for _, w := range []Window{l.Daily, l.Hourly} {
		if w.Remaining <= LimitFloor && now.Before(w.Reset) {
			return rateLimitError(w.Reset)
		}
	}
	return nil
}

func (c *Client) record(h http.Header) {
	get := func(name string) (int, bool) {
		n, err := strconv.Atoi(h.Get(name))
		return n, err == nil
	}
	dr, ok1 := get("X-Rl-Daily-Remaining")
	hr, ok2 := get("X-Rl-Hourly-Remaining")
	if !ok1 || !ok2 {
		return
	}
	dl, _ := get("X-Rl-Daily-Limit")
	hl, _ := get("X-Rl-Hourly-Limit")
	v := Limits{
		Known:  true,
		Daily:  Window{Remaining: dr, Limit: dl, Reset: parseReset(h.Get("X-Rl-Daily-Reset"))},
		Hourly: Window{Remaining: hr, Limit: hl, Reset: parseReset(h.Get("X-Rl-Hourly-Reset"))},
	}
	c.lim.mu.Lock()
	c.lim.v = v
	c.lim.mu.Unlock()
	// The hook runs unlocked: it reads the budget back through Limits, which takes the same lock.
	if c.onLimits != nil {
		c.onLimits(v)
	}
}

func parseReset(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05 MST"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func (c *Client) roundTrip(ctx context.Context, method, path string, body any) (int, string, []byte, error) {
	if err := c.blocked(); err != nil {
		return 0, "", nil, err
	}
	base := cmp.Or(c.BaseURL, BaseURL)
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, "", nil, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
	if err != nil {
		return 0, "", nil, err
	}
	if err := c.authorize(ctx, req); err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Application-Name", c.name)
	req.Header.Set("Application-Version", c.version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Go forwards a custom header such as Apikey to wherever a redirect points.
	hc := httpkit.WithRedirect(cmp.Or(c.HTTP, http.DefaultClient), httpkit.SameOrigin)
	resp, err := hc.Do(req)
	if ue, ok := errors.AsType[*url.Error](err); ok {
		// A link request carries the nxm key and expiry in its query; the error quotes the URL into logs.
		ue.URL, _, _ = strings.Cut(ue.URL, "?")
	}
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	c.record(resp.Header)
	if resp.StatusCode == http.StatusTooManyRequests {
		return 0, "", nil, c.rateLimited(resp.Header)
	}
	raw, err := safefs.ReadCapped(resp.Body, maxBody)
	if err != nil {
		return 0, "", nil, err
	}
	return resp.StatusCode, resp.Status, raw, nil
}

// rateLimited builds the error for a 429: the X-Rl-* reset when Nexus sent one still in the future, else Retry-After, else no reset (the
// queue then applies its own backoff).
func (c *Client) rateLimited(h http.Header) error {
	reset := c.Limits().Hourly.Reset
	if d := c.Limits().Daily; d.Remaining <= 0 {
		reset = d.Reset
	}
	if !reset.After(c.now()) {
		reset, _ = httpkit.RetryAfter(h.Get("Retry-After"))
	}
	return rateLimitError(reset)
}

// get decodes a 200 JSON answer for path into out; map403 turns a 403 into ErrPremiumRequired.
func (c *Client) get(ctx context.Context, path string, map403 bool, out any) error {
	code, status, raw, err := c.roundTrip(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	switch {
	case code == http.StatusOK:
	case code == http.StatusUnauthorized:
		return ErrUnauthorized
	case code == http.StatusForbidden && map403:
		return ErrPremiumRequired
	default:
		return statusError(code, status)
	}
	return json.Unmarshal(raw, out)
}

// User is the account a key belongs to.
type User struct {
	ID        int    `json:"userId"`
	Name      string `json:"name"`
	IsPremium bool   `json:"isPremium"`
}

// Validate identifies the account behind the client's key.
func (c *Client) Validate(ctx context.Context) (User, error) {
	var raw struct {
		ID        int    `json:"user_id"`
		Name      string `json:"name"`
		IsPremium bool   `json:"is_premium"`
	}
	if err := c.get(ctx, "/v1/users/validate.json", false, &raw); err != nil {
		return User{}, err
	}
	return User{ID: raw.ID, Name: raw.Name, IsPremium: raw.IsPremium}, nil
}

// File is one downloadable file of a mod; Category is empty when Nexus reports null. Name is its title on the
// page and Description its BBCode.
type File struct {
	FileID      int       `json:"fileId"`
	FileName    string    `json:"fileName"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	ModVersion  string    `json:"modVersion"`
	Category    string    `json:"category"`
	SizeKB      int64     `json:"sizeKb"`
	IsPrimary   bool      `json:"isPrimary"`
	Uploaded    time.Time `json:"uploaded"`
	MD5         string    `json:"md5,omitempty"`
	// ReplacedBy is the file the author uploaded as this one's update (Nexus file_updates), or 0.
	ReplacedBy int `json:"replacedBy"`
}

// graphql posts a v2 query and returns the answer's body. A refused key and a spent quota come back as the errors the
// REST calls give, so every caller tells them apart the same way.
func (c *Client) graphql(ctx context.Context, query any) ([]byte, error) {
	code, status, body, err := c.roundTrip(ctx, http.MethodPost, "/v2/graphql", query)
	if err != nil {
		return nil, err
	}
	switch code {
	case http.StatusOK:
		return body, nil
	case http.StatusUnauthorized:
		return nil, ErrUnauthorized
	}
	return nil, statusError(code, status)
}

// ScanStatuses returns Nexus's v2 virus-scan status for each file. Results are cached for this client.
func (c *Client) ScanStatuses(ctx context.Context, t Title, modID int) (map[int]string, error) {
	c.scanMu.Lock()
	if statuses, ok := c.scans[scanKey{t.ID, modID}]; ok {
		c.scanMu.Unlock()
		return statuses, nil
	}
	c.scanMu.Unlock()

	body, err := c.graphql(ctx, map[string]string{
		"query": fmt.Sprintf("{ modFiles(modId: %d, gameId: %d) { fileId scannedV2 } }", modID, t.ID),
	})
	if err != nil {
		return nil, err
	}
	var raw struct {
		Data struct {
			Files []struct {
				FileID  int    `json:"fileId"`
				Scanned string `json:"scannedV2"`
			} `json:"modFiles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	statuses := make(map[int]string, len(raw.Data.Files))
	for _, file := range raw.Data.Files {
		statuses[file.FileID] = file.Scanned
	}
	c.scanMu.Lock()
	c.scans[scanKey{t.ID, modID}] = statuses
	c.scanMu.Unlock()
	return statuses, nil
}

// Files lists every file of a mod.
func (c *Client) Files(ctx context.Context, t Title, modID int) ([]File, error) {
	var raw struct {
		Files []struct {
			FileID      int       `json:"file_id"`
			FileName    string    `json:"file_name"`
			Name        string    `json:"name"`
			Description string    `json:"description"`
			Version     string    `json:"version"`
			ModVersion  string    `json:"mod_version"`
			Category    *string   `json:"category_name"`
			SizeKB      int64     `json:"size_kb"`
			IsPrimary   bool      `json:"is_primary"`
			Uploaded    time.Time `json:"uploaded_time"`
			MD5         string    `json:"md5"`
			MD5Hash     string    `json:"md5_hash"`
		} `json:"files"`
		Updates []struct {
			Old int `json:"old_file_id"`
			New int `json:"new_file_id"`
		} `json:"file_updates"`
	}
	if err := c.get(ctx, fmt.Sprintf("/v1/games/%s/mods/%d/files.json", t.Domain, modID), false, &raw); err != nil {
		return nil, err
	}
	files := make([]File, 0, len(raw.Files))
	for _, f := range raw.Files {
		file := File{
			FileID: f.FileID, FileName: f.FileName, Name: f.Name, Description: f.Description, Version: f.Version,
			ModVersion: f.ModVersion, SizeKB: f.SizeKB, IsPrimary: f.IsPrimary, Uploaded: f.Uploaded.UTC(),
			MD5: cmp.Or(strings.TrimSpace(f.MD5), strings.TrimSpace(f.MD5Hash)),
		}
		if f.Category != nil {
			file.Category = *f.Category
		}
		files = append(files, file)
	}
	for _, u := range raw.Updates {
		for i := range files {
			if files[i].FileID == u.Old && u.New > files[i].ReplacedBy {
				files[i].ReplacedBy = u.New
			}
		}
	}
	return files, nil
}

// Link is one download mirror.
type Link struct {
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	URI       string `json:"uri"`
}

type rawLink struct {
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	URI       string `json:"URI"`
}

func (c *Client) downloadLinks(ctx context.Context, path string) ([]Link, error) {
	var raw []rawLink
	if err := c.get(ctx, path, true, &raw); err != nil {
		return nil, err
	}
	links := make([]Link, len(raw))
	for i, l := range raw {
		links[i] = Link(l)
	}
	return links, nil
}

func downloadPath(t Title, modID, fileID int) string {
	return fmt.Sprintf("/v1/games/%s/mods/%d/files/%d/download_link.json", t.Domain, modID, fileID)
}

// DownloadLinks asks for a file's download mirrors in Nexus's order, redeeming the key and expiry of an nxm:// link
// that ParseNXM accepted. A link with no key or no expiry is refused before any request, so a hand-built literal
// cannot fall through to the keyless form.
func (c *Client) DownloadLinks(ctx context.Context, t Title, l NXMLink) ([]Link, error) {
	if l.Key == "" || l.Expires <= 0 {
		return nil, errors.New("nexus: the nxm link has no key or expiry")
	}
	q := url.Values{"key": {l.Key}, "expires": {strconv.FormatInt(l.Expires, 10)}}
	return c.downloadLinks(ctx, downloadPath(t, l.ModID, l.FileID)+"?"+q.Encode())
}

// PremiumDownloadLinks asks for a file's download mirrors with no key, which Nexus allows only premium accounts; a
// free account gets ErrPremiumRequired.
func (c *Client) PremiumDownloadLinks(ctx context.Context, t Title, modID, fileID int) ([]Link, error) {
	return c.downloadLinks(ctx, downloadPath(t, modID, fileID))
}
