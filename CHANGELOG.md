# Changelog

Notable changes to `go-nexusmods`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Rethunk-Tech/go-nexusmods/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Rethunk-Tech/go-nexusmods/releases/tag/v0.1.0
