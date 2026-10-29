<h1 align="center">go-nexusmods</h1>

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27.3+-00ADD8?logo=go&logoColor=white)](go.mod)

</div>

---

A Go client for the Nexus Mods API: REST and GraphQL calls, the rate-limit budget, nxm:// links and the two sign-in flows. Package `nexusmods`, module `github.com/Rethunk-Tech/go-nexusmods`.

Nexus asks every app to say who it is and to stay inside its hourly and daily budget. This client does both by
construction: it sends the name and version you give it on every request, reads the `X-Rl-*` headers, and refuses to
send once a window is nearly spent. It never fetches a mod file: the free path needs an `nxm://` link from Nexus's own
Mod Manager Download button, and the premium paths are named `Premium...` so `grep Premium` finds each. The caller sets
the User-Agent (an `httpkit.Transport` does it) and owns token storage; `WithKey` and `WithBearer` copies share one
budget.

## Quick start

```bash
go get github.com/Rethunk-Tech/go-nexusmods
```

A worked example and the contracts: [HUMANS.md](HUMANS.md).

## Features

- REST and GraphQL reads: account, mod pages, files, changelogs, categories, scan status, tracked mods, collections.
- Batched lookups of many mods or files, one request per 50.
- `nxm://` and mod-page link parsing that refuses anything but the exact forms.
- A live rate-limit budget and typed errors (`httpkit.StatusError`, `httpkit.RateLimitError`).
- Sign-in in package `sso`: legacy SSO and OAuth 2 PKCE with a loopback callback.

## Documentation

| Doc | Audience |
| --- | --- |
| [HUMANS.md](HUMANS.md) | Running and using the package |
| [AGENTS.md](AGENTS.md) | Internals, invariants, and why |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Process — commits, tests, docs |
| [SECURITY.md](SECURITY.md) | Vulnerability disclosure |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |

## License

MIT — see [LICENSE](LICENSE). © 2026 Rethunk.Tech, LLC.
