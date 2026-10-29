# Security

## Supported versions

The latest tagged release. There are no long-lived release branches, so a fix ships in the next tag
rather than being backported.

## Reporting a vulnerability

Use GitHub's **private vulnerability reporting** on this repository (Security → Report a
vulnerability). It stays private until a fix ships.

Please do not open a public issue for a suspected vulnerability first.

## What is in scope

- Response bodies from the API (`Validate`, `Files`, `Page`, `Changelogs`, batch calls, `Collection`): capped at
  16 MiB, and no body may panic a decoder (the fuzz targets).
- `PremiumCollectionArchive`: the mirror address comes from the API response. Only `https://` is fetched, the body is
  capped at 64 MiB, and a silent mirror is abandoned.
- Collection links (`Collection`): a non-http(s) URL never reaches the caller.
- `ParseNXM`, `NXMDomain`, `ParseModURL`: pasted or remote text; only the exact forms are accepted, and `ParseNXM`
  refuses a link for another user or an expired one.
- Credentials: a redirect to another origin is refused, and a transport error carries no `nxm` key or expiry.
- `sso.OAuth`: the callback accepts only its own `state`; the token body is capped at 64 KiB; PKCE S256.

## What is out of scope

- The caller supplies the API key, tokens, `Application-Name` and the `GameOf` mapping, and stores tokens safely.
- Nexus enforces download rights: an `NXMLink` literal built by hand is refused by Nexus, not by this package.
- The loopback callback is plain http on the address the caller registered with Nexus.
