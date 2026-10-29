# AGENTS.md

Package `nexusmods` is a client for the Nexus Mods API (v1 REST, v2 GraphQL), its `X-Rl-*` budget and `nxm://` links;
`sso` signs a user in. Mod managers, update checkers and bots import it. It imports no consumer package and does no
file I/O; wording a player reads, product settings and caches belong to the caller.

## Gate

`gate` is the command. Each override in `.gate.toml` is there because a runner
cannot resolve that check from PATH. The `windows-latest` test job is the only place
Windows-only behaviour runs; say here which tests depend on it.
`sso`'s `TestBusyPortFailsWithoutFallingBack` is the one that reaches the `WSAEADDRINUSE` branch of `isAddrInUse`.

## Shape

- `roundTrip` is the single path for every API call. It refuses at `LimitFloor`, sets the identity headers, strips an
  `nxm` key from a transport `*url.Error` (the error text reaches logs), stops a 429 before reading the body, and
  records the budget. A new endpoint goes through `get`, `do` or `graphql`, never `http.Client` directly.
- Errors are httpkit's types, wrapped `"nexus: %w"`: a 401 is `ErrUnauthorized`, never a `StatusError`; a 429 or a
  refusal at the floor is a `*httpkit.RateLimitError`. The package carries no player-facing wording, so a caller
  words its own.
- The redirect policy is `httpkit.SameOrigin`, chained onto the caller's client, so `Apikey` and `Authorization`
  stay on the API origin.
- No function returns mod-file bytes. `DownloadLinks` refuses a link with no key or expiry before any request so a
  literal cannot fall through to the keyless form; the keyless forms are `PremiumDownloadLinks` and
  `PremiumCollectionArchive`.
- `PremiumCollectionArchive` does not use `httpkit.DoCapped`: the mirror is outside Nexus's API and may stall, so
  silence is bounded by `mirrorTimeout` (before headers and between reads, never a total deadline), and a stall
  fails the fetch even if the body then ends cleanly. Only `/v2/collections/` paths without `..` and `https://`
  mirrors are accepted.
- `decodeCollection` drops every non-http(s) URL: curator text is remote data a caller may open with a URL handler.
- `Collection`, `Page`, `File`, `Requirement`, `TrackedMod`, `Window`, `Limits` and `NXMLink` are stored or printed
  by callers: their JSON tags (`Collection` has none) are a format, pinned by `TestJSONFormsAreStable`.
- Every `limiter` callback runs unlocked (`TestSourceDoesNotInvokeCallbacksUnderMutex`).
- `sso.refreshMu` is package-level because Nexus rotates the refresh token on use and callers build an `OAuth` value
  per call. `Fresh` treats an empty `Access` as expired, so a store may drop the access token to fit a size limit.
- `OAuth.Addr` has no default: Nexus registers a fixed callback, and a random port would fail only at Nexus.
  `ErrPortBusy` is returned only for address-in-use.

## Files

| File | Holds |
| --- | --- |
| `doc.go` | Package doc |
| `client.go` | `Client`, the budget, `roundTrip`, `get`, `graphql`, `Validate`, `Files`, `ScanStatuses`, download links |
| `auth.go` | `WithKey`, `WithBearer`, `TokenSource` |
| `account.go` | Endorse, abstain, tracked mods and their memo |
| `mod.go` | `Page`, `Changelogs`, `Categories` |
| `batch.go`, `filesbatch.go` | Batched GraphQL lookups of mods and files |
| `collection.go` | `Collection`, its decoder, `PremiumCollectionArchive` |
| `nxm.go` | `ParseNXM`, `NXMLink`, `NXMDomain`, `RejectError` |
| `url.go`, `version.go` | Mod-page URLs, `NewerFile` |
| `sso/sso.go` | `Legacy`, the sign-in states and errors |
| `sso/oauth.go` | `OAuth`, `Tokens`, `TokenStore`, `Fresh` |
