package nexusmods

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/Rethunk-Tech/go-safefs"
)

const (
	// SingleUploadMax is the largest file sent in one PUT; a bigger one goes up in the parts Nexus asks for.
	SingleUploadMax = 100 << 20
	// maxUploadParts is S3's limit on the parts of one upload.
	maxUploadParts = 10000
	// uploadPolls bounds the wait for Nexus to process an upload, at a growing interval.
	uploadPolls   = 40
	maxPollWait   = 15 * time.Second
	defaultPoll   = 2 * time.Second
	uploadAPIPath = "/v3"
)

// Uploader publishes a file to Nexus Mods through its v3 API, as Nexus's own upload action does: the file goes to
// storage in one PUT or in parts, Nexus processes it, and it becomes the next version of an existing mod file.
type Uploader struct {
	c *Client
	// PollInterval is the first wait between checks of an upload being processed; each wait is one longer, up to 15
	// seconds. Zero means 2 seconds.
	PollInterval time.Duration
	// singleMax is the largest file sent in one PUT; zero is SingleUploadMax.
	singleMax int64
}

// NewUploader returns an Uploader that sends its API calls through c, so they carry c's credential, identity headers
// and redirect policy and count against its rate limit. Storage requests, which go to addresses Nexus hands out, carry
// none of them.
func NewUploader(c *Client) *Uploader { return &Uploader{c: c} }

// Release is one new version of an existing mod file.
type Release struct {
	// Path is the archive to upload; it must be a regular file.
	Path string
	// ModFileID is the mod file the upload becomes the next version of; the one it replaces is archived.
	ModFileID string
	// Name and Version are the file's name and version on the mod page.
	Name, Version string
	// ModID and Changelog add Changelog under the mod's page for Version; both must be set, else no changelog is added.
	ModID, Changelog string
}

type uploadSession struct {
	Data struct {
		ID           string   `json:"id"`
		State        string   `json:"state"`
		PresignedURL string   `json:"presigned_url"`
		PartSize     int64    `json:"part_size_bytes"`
		PartURLs     []string `json:"part_presigned_urls"`
		CompleteURL  string   `json:"complete_presigned_url"`
	} `json:"data"`
}

// call sends one v3 request through the client and decodes a 2xx answer into out when out is not nil.
func (u *Uploader) call(ctx context.Context, method, path string, body, out any) error {
	code, status, raw, err := u.c.roundTrip(ctx, method, uploadAPIPath+path, body)
	if err != nil {
		return err
	}
	switch {
	case code == http.StatusUnauthorized:
		return ErrUnauthorized
	case code >= http.StatusBadRequest:
		return statusError(code, status)
	case out == nil:
		return nil
	}
	return json.Unmarshal(raw, out)
}

// send PUTs or POSTs body to a storage address Nexus gave and returns the ETag header of the answer.
func (u *Uploader) send(ctx context.Context, method, rawURL, contentType string, body io.Reader, size int64) (string, error) {
	target, err := storageURL(rawURL, cmp.Or(u.c.BaseURL, BaseURL))
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return "", err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	res, err := httpkit.WithRedirect(cmp.Or(u.c.HTTP, http.DefaultClient), httpkit.NoDowngrade).Do(req)
	if ue, ok := errors.AsType[*url.Error](err); ok {
		// A presigned address carries its signature in the query; the error quotes the URL into logs.
		ue.URL, _, _ = strings.Cut(ue.URL, "?")
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusTooManyRequests {
		reset, _ := httpkit.RetryAfter(res.Header.Get("Retry-After"))
		return "", rateLimitError(reset)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxBody))
	if res.StatusCode >= http.StatusBadRequest {
		return "", statusError(res.StatusCode, res.Status)
	}
	return strings.Trim(res.Header.Get("ETag"), `"`), nil
}

// storageURL is an address Nexus handed out to upload bytes to. The file must not cross the internet in the clear: only
// https is followed, or plain http when the API itself was reached that way on the same host.
func storageURL(raw, apiBase string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("nexus: gave an unusable upload address")
	}
	if api, err := url.Parse(apiBase); u.Scheme != "https" && (err != nil || api.Scheme != "http" || u.Host != api.Host) {
		return "", errors.New("nexus: gave an upload address that is not https")
	}
	return raw, nil
}

// store creates an upload session for the file and sends its bytes, then returns the upload's id.
func (u *Uploader) store(ctx context.Context, path string) (string, error) {
	f, err := safefs.OpenRegular(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	req := map[string]any{"filename": filepath.Base(path), "size_bytes": size}
	var session uploadSession
	if size <= cmp.Or(u.singleMax, SingleUploadMax) {
		if err := u.call(ctx, http.MethodPost, "/uploads", req, &session); err != nil {
			return "", err
		}
		d := session.Data
		if d.ID == "" || d.PresignedURL == "" {
			return "", errors.New("nexus: answered without an upload address")
		}
		_, err := u.send(ctx, http.MethodPut, d.PresignedURL, "application/octet-stream", io.NewSectionReader(f, 0, size), size)
		return d.ID, err
	}
	if err := u.call(ctx, http.MethodPost, "/uploads/multipart", req, &session); err != nil {
		return "", err
	}
	d := session.Data
	// Compared as a quotient so a huge part size cannot overflow a product.
	need := size / max(d.PartSize, 1)
	if size%max(d.PartSize, 1) != 0 {
		need++
	}
	if d.ID == "" || d.CompleteURL == "" || d.PartSize <= 0 || len(d.PartURLs) > maxUploadParts || int64(len(d.PartURLs)) != need {
		return "", errors.New("nexus: asked for parts that do not cover the file")
	}
	var done strings.Builder
	done.WriteString("<CompleteMultipartUpload>\n")
	for i, partURL := range d.PartURLs {
		offset := int64(i) * d.PartSize
		length := min(d.PartSize, size-offset)
		if length <= 0 {
			return "", fmt.Errorf("nexus: asked for part %d beyond the file", i+1)
		}
		etag, err := u.send(ctx, http.MethodPut, partURL, "application/octet-stream", io.NewSectionReader(f, offset, length), length)
		if err != nil {
			return "", err
		}
		if etag == "" {
			return "", fmt.Errorf("nexus: uploading part %d: no ETag", i+1)
		}
		fmt.Fprintf(&done, "  <Part>\n    <PartNumber>%d</PartNumber>\n    <ETag>%s</ETag>\n  </Part>\n", i+1, etag)
	}
	done.WriteString("</CompleteMultipartUpload>")
	_, err = u.send(ctx, http.MethodPost, d.CompleteURL, "application/xml", strings.NewReader(done.String()), int64(done.Len()))
	return d.ID, err
}

// ErrUploadPending means Nexus was still processing an upload when the wait ran out.
var ErrUploadPending = errors.New("nexus is still processing the upload")

// wait polls the upload until Nexus has processed it.
func (u *Uploader) wait(ctx context.Context, id string) error {
	poll := cmp.Or(u.PollInterval, defaultPoll)
	for attempt := 1; attempt <= uploadPolls; attempt++ {
		var up uploadSession
		if err := u.call(ctx, http.MethodGet, "/uploads/"+url.PathEscape(id), nil, &up); err != nil {
			return err
		}
		if up.Data.State == "available" {
			return nil
		}
		t := time.NewTimer(min(poll*time.Duration(attempt), maxPollWait))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return ErrUploadPending
}

// ChangelogError is Upload's failure to add the changelog after the file was uploaded and versioned: the file is up,
// and only the changelog needs retrying.
type ChangelogError struct{ Err error }

func (e *ChangelogError) Error() string {
	return "the file is up, but its changelog was not added: " + e.Err.Error()
}

func (e *ChangelogError) Unwrap() error { return e.Err }

// Upload sends r.Path as the next version of r.ModFileID and waits for Nexus to process it. When r.ModID and
// r.Changelog are set it then adds the changelog; if that last step fails the file is already up, and the error says
// so.
func (u *Uploader) Upload(ctx context.Context, r Release) error {
	id, err := u.store(ctx, r.Path)
	if err != nil {
		return err
	}
	if err := u.call(ctx, http.MethodPost, "/uploads/"+url.PathEscape(id)+"/finalise", nil, nil); err != nil {
		return err
	}
	if err := u.wait(ctx, id); err != nil {
		return err
	}
	err = u.call(ctx, http.MethodPost, "/mod-files/"+url.PathEscape(r.ModFileID)+"/versions", map[string]any{
		"upload_id": id, "name": r.Name, "version": r.Version, "file_category": "main", "archive_existing_file": true,
	}, nil)
	if err != nil || r.Changelog == "" || r.ModID == "" {
		return err
	}
	if err := u.call(ctx, http.MethodPost, "/mods/"+url.PathEscape(r.ModID)+"/changelogs", map[string]any{"version": r.Version, "changelog": r.Changelog}, nil); err != nil {
		return &ChangelogError{Err: err}
	}
	return nil
}
