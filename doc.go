// Package nexusmods is a client for the Nexus Mods API: the v1 REST calls and v2 GraphQL queries a mod manager
// makes, the rate-limit budget Nexus reports in its X-Rl-* headers, nxm:// link parsing and mod-page URLs.
//
// Every request carries the Application-Name and Application-Version the caller gave New, and none is sent once a
// window is at LimitFloor until it resets. No function returns a mod file's bytes: DownloadLinks redeems an nxm://
// link from Nexus's Mod Manager Download button, PremiumDownloadLinks asks as a premium account, and fetching a
// mirror is the caller's code. The package sets no User-Agent and opens no browser; the sign-in flows live in the
// sso subpackage.
package nexusmods
