# Changelog

Notable changes to `go-nexusmods`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- The OAuth token exchange no longer follows redirects, so a 307 cannot re-send the refresh token to another origin.
- `PremiumCollectionArchive` takes only plain `/v2/collections/` segments (a percent-encoded `..` reached the API with the bearer token), drops the signed query from mirror errors and refuses an https-to-http mirror redirect.
- An `expires_in` below zero or over a year is an error instead of an expiry in the past.
- A refresh whose `Store.Save` failed retries once, then keeps the new grant in memory for the next `Fresh` and returns an error saying it was not stored.
- `sso.OAuth.LockFile` makes `Fresh` hold an exclusive lock file across load, refresh and save, so two processes sharing one store cannot spend the same rotated refresh token.
- `sso.OAuth` and `sso.Legacy` refuse an `http` or `ws` endpoint override unless it is loopback.
- `ParseNXM` refuses a repeated `expires` or `user_id`.

### Added

- `Client.Search` lists a page of a game's mods through the v2 GraphQL API (`SearchQuery`: name text, sort, any-of and excluded categories, count and offset; `SearchResult`, `SearchMod`).

## [0.5.0] - 2026-11-04

### Added

- `sso.OAuth.Identify` names the account behind an access token, from Nexus's `/oauth/userinfo`. `Client.Validate` answers personal API keys only: `/v1/users/validate.json` refuses an access token.

### Changed

- `ErrUnauthorized` reads "nexus rejected the sign-in", since it also covers a refused access token.

## [0.4.0] - 2026-11-02

### Added

- `ChangelogError`, the error `Upload` returns when the file is up but its changelog was not added; `errors.As` tells it from a failed upload, and `Unwrap` gives the cause.

### Changed

- A multipart upload is refused before its first PUT unless Nexus's part count is exactly the size divided by the part size, rounded up.

### Fixed

- A storage request's transport error no longer quotes the presigned signature, and a 429 from storage is an `*httpkit.RateLimitError` like the API's.

## [0.3.0] - 2026-11-02

### Added

- `Uploader`, built by `NewUploader` over a `Client`, publishes a file as the next version of a mod file through the v3 API: one PUT or S3 multipart parts, a wait for Nexus to process it, then the version and an optional changelog. Storage addresses must be https, and the parts Nexus asks for must cover the file.

## [0.2.0] - 2026-10-30

### Added

- `ParseArchiveName` reads the mod name, mod id, version, timestamp and extension out of a Nexus download's file name.

## [0.1.0] - 2026-10-29

### Added

- `nexusmods.Client`: account, mod pages, files, changelogs, categories, scan statuses, endorsements, tracked mods
  and collections, with the `X-Rl-*` budget refused at `LimitFloor` and typed `httpkit` errors.
- Batched `ModsByDomain` and `FilesOf` lookups, one GraphQL request per 50.
- `DownloadLinks` for an `nxm://` link, `PremiumDownloadLinks` and `PremiumCollectionArchive` for premium accounts;
  no mod file is ever fetched.
- `ParseNXM`, `NXMDomain`, `ModURL`, `ParseModURL` and `NewerFile`.
- Package `sso`: legacy SSO (`Legacy`) and OAuth 2 PKCE (`OAuth`) with a caller-supplied `TokenStore`.

[Unreleased]: https://github.com/Rethunk-Tech/go-nexusmods/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/Rethunk-Tech/go-nexusmods/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/Rethunk-Tech/go-nexusmods/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Rethunk-Tech/go-nexusmods/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Rethunk-Tech/go-nexusmods/releases/tag/v0.1.0
