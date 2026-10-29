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
	"strings"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/Rethunk-Tech/go-safefs"
)

// MaxCollectionArchive caps the curator's collection archive PremiumCollectionArchive reads into memory.
const MaxCollectionArchive = 64 << 20

// mirrorTimeout is how long the mirror, which sits outside Nexus's API and can stall, may stay silent: before the
// headers and between body reads. A slow but steady link is never cut off.
var mirrorTimeout = 2 * time.Minute

// PremiumCollectionArchive downloads a collection revision's curator archive (collection.json plus bundled files).
// downloadLink is Collection's DownloadLink, the API path Nexus reports for the revision. It asks with no key, which
// Nexus allows only premium accounts, and a free account gets ErrPremiumRequired. The archive is the curator's
// manifest, not a mod file, and it is the one body this package downloads from outside Nexus's API.
func (c *Client) PremiumCollectionArchive(ctx context.Context, downloadLink string) ([]byte, error) {
	if !strings.HasPrefix(downloadLink, "/v2/collections/") || strings.Contains(downloadLink, "..") {
		return nil, fmt.Errorf("unexpected collection download link %q", downloadLink)
	}
	var res struct {
		Links []struct {
			URI string `json:"URI"`
		} `json:"download_links"`
		Link struct {
			URI string `json:"URI"`
		} `json:"download_link"`
	}
	if err := c.get(ctx, downloadLink, true, &res); err != nil {
		return nil, err
	}
	uri := res.Link.URI
	if len(res.Links) > 0 {
		uri = res.Links[0].URI
	}
	if !strings.HasPrefix(uri, "https://") {
		return nil, errors.New("nexus gave no collection download link")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(mirrorTimeout, func() { cancel(errMirrorStalled) })
	defer stall.Stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	hc := cmp.Or(c.HTTP, http.DefaultClient)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, cmp.Or(context.Cause(ctx), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusError(resp.StatusCode, resp.Status)
	}
	b, err := safefs.ReadCapped(idleReader{resp.Body, stall}, MaxCollectionArchive)
	// A stall cancels the request, and the mirror may then end the body cleanly before the read sees the cancel.
	if err == nil {
		err = context.Cause(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("the collection archive: %w", cmp.Or(context.Cause(ctx), err))
	}
	return b, nil
}

var errMirrorStalled = errors.New("the collection mirror stopped sending data")

// idleReader restarts the stall timer on every read that returns bytes.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
}

func (i idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.timer.Reset(mirrorTimeout)
	}
	return n, err
}

const collectionQuery = `query($slug:String!,$domain:String,$revision:Int){collectionRevision(slug:$slug,domainName:$domain,revision:$revision,viewAdultContent:true){revisionNumber downloadLink installationInfo externalResources{name resourceType resourceUrl optional version author} collection{name slug user{name}} modFiles{fileId optional file{modId name version mod{name}}}}}`

// Collection is one revision of a Nexus collection. It has no JSON tags: the Go field names are its stored form.
type Collection struct {
	Name     string
	Author   string
	Slug     string
	Revision int
	Files    []CollectionFile
	// Instructions is the curator's installationInfo text, shown before importing.
	Instructions string
	// External lists what the curator points to outside Nexus; each URL is an http or https address or empty.
	External []CollectionExternal
	// DownloadLink is the Nexus API path that resolves to the curator's collection archive.
	DownloadLink string
}

// CollectionExternal is one off-Nexus resource of a collection. Type is Nexus's resourceType ("direct", "browse" or "manual").
type CollectionExternal struct {
	Name     string
	Type     string
	URL      string
	Version  string
	Author   string
	Optional bool
}

// CollectionFile is one mod file a collection installs.
type CollectionFile struct {
	ModID    int
	FileID   int
	Optional bool
}

// Collection reads a collection revision, the latest when revision is 0. A collection Nexus does not know wraps
// httpkit.ErrNotFound.
func (c *Client) Collection(ctx context.Context, domain, slug string, revision int) (Collection, error) {
	vars := map[string]any{"slug": slug, "domain": domain}
	if revision != 0 {
		vars["revision"] = revision
	}
	body, err := c.graphql(ctx, map[string]any{"query": collectionQuery, "variables": vars})
	if err != nil {
		return Collection{}, err
	}
	return decodeCollection(slug, body)
}

type collectionGQL struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Data *struct {
		CollectionRevision *struct {
			RevisionNumber    int    `json:"revisionNumber"`
			DownloadLink      string `json:"downloadLink"`
			InstallationInfo  string `json:"installationInfo"`
			ExternalResources []struct {
				Name         string `json:"name"`
				ResourceType string `json:"resourceType"`
				ResourceURL  string `json:"resourceUrl"`
				Version      string `json:"version"`
				Author       string `json:"author"`
				Optional     bool   `json:"optional"`
			} `json:"externalResources"`
			Collection struct {
				Name string `json:"name"`
				Slug string `json:"slug"`
				User struct {
					Name string `json:"name"`
				} `json:"user"`
			} `json:"collection"`
			ModFiles []struct {
				FileID   int  `json:"fileId"`
				Optional bool `json:"optional"`
				File     *struct {
					ModID int `json:"modId"`
				} `json:"file"`
			} `json:"modFiles"`
		} `json:"collectionRevision"`
	} `json:"data"`
}

// webLink is raw when it is an http or https address with a host, else empty: a curator-written link is remote data
// that a caller may hand to a URL handler, which would launch other schemes' apps.
func webLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return raw
}

func decodeCollection(slug string, raw []byte) (Collection, error) {
	var parsed collectionGQL
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Collection{}, err
	}
	if len(parsed.Errors) > 0 || parsed.Data == nil || parsed.Data.CollectionRevision == nil {
		return Collection{}, fmt.Errorf("nexus has no collection %q: %w", slug, httpkit.ErrNotFound)
	}
	rev := parsed.Data.CollectionRevision
	out := Collection{
		Name:     rev.Collection.Name,
		Author:   rev.Collection.User.Name,
		Slug:     rev.Collection.Slug,
		Revision: rev.RevisionNumber,

		Instructions: strings.TrimSpace(rev.InstallationInfo),
		DownloadLink: rev.DownloadLink,
	}
	for _, r := range rev.ExternalResources {
		out.External = append(out.External, CollectionExternal{
			Name: r.Name, Type: r.ResourceType, URL: webLink(r.ResourceURL), Version: r.Version, Author: r.Author, Optional: r.Optional,
		})
	}
	for _, mf := range rev.ModFiles {
		if mf.File == nil {
			continue
		}
		out.Files = append(out.Files, CollectionFile{
			ModID:    mf.File.ModID,
			FileID:   mf.FileID,
			Optional: mf.Optional,
		})
	}
	return out, nil
}
