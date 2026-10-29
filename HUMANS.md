# HUMANS.md

Run and use `go-nexusmods`. Internals: [AGENTS.md](AGENTS.md).

## Quick start

```bash
go get github.com/Rethunk-Tech/go-nexusmods
```

```go
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/Rethunk-Tech/go-nexusmods"
)

func main() {
	c := nexusmods.New("MyApp", "1.0.0")
	c.HTTP = &http.Client{Transport: &httpkit.Transport{UserAgent: "MyApp/1.0.0"}, Timeout: 30 * time.Second}
	c = c.WithKey(os.Getenv("NEXUS_API_KEY"))

	stardew := nexusmods.Title{Domain: "stardewvalley", ID: 1303}
	files, err := c.Files(context.Background(), stardew, 541)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, f := range files {
		fmt.Println(f.FileID, f.Name, f.Version)
	}
	fmt.Println(c.Limits().Hourly.Remaining)
}
```

## Contracts

| Behaviour | Contract |
| --- | --- |
| `New(name, version)` | Every request carries `Application-Name` and `Application-Version`; no credential until `WithKey` or `WithBearer`. |
| `WithKey`, `WithBearer` | The copy shares the budget and caches. A bearer token is asked for on every request. |
| User-Agent, reachability | Not set here: give `Client.HTTP` an `httpkit.Transport`; its `OnResult` sees every round trip, the archive mirror's included. |
| Budget | No request is sent while a window has `LimitFloor` or fewer calls left and has not reset; the refusal is a `*httpkit.RateLimitError`. `SetLimitsHook` runs unlocked. |
| Errors | 401 is `ErrUnauthorized`; 429 is `*httpkit.RateLimitError` (reset from `X-Rl-*`, else `Retry-After`, else zero); other non-2xx is `*httpkit.StatusError`, or `*MessageError` when Nexus sent a message. |
| `DownloadLinks` | Redeems an `NXMLink`; refuses an empty `Key` or `Expires <= 0` before any request. Returns Nexus's mirror order. |
| `PremiumDownloadLinks`, `PremiumCollectionArchive` | Ask with no key; a free account gets `ErrPremiumRequired`. |
| Mod files | Never fetched or written by this package. |
| `ParseNXM` | Accepts only `nxm://<domain>/mods/<id>/files/<id>?key=&expires=&user_id=` for the signed-in user, unexpired; every refusal is a `*RejectError` with a `Reason*`. |
| `Collection` | Latest revision when `revision` is 0; a missing one wraps `httpkit.ErrNotFound`; non-http(s) links are blanked. |
| `ModsByDomain`, `FilesOf` | 50 ids per request; a failure returns what was found with the error. |
| `sso.Legacy.Run` | Returns the API key; redials a dropped socket, not a refusal. |
| `sso.OAuth` | `Addr` is required; `Authorize` fails before opening the browser if it cannot listen; `Fresh` refreshes inside a minute of expiry through your `TokenStore`; a refused refresh is `ErrRevoked`. |

## Requirements

Go 1.27.3 or newer, plus `lefthook` and `gitleaks` on PATH if you want the git hooks.

```bash
lefthook install
```

That is per clone; git does not commit hooks. Pre-commit scans staged changes for secrets, and
pre-push runs `gate`.

## Verify

```bash
gate
```
