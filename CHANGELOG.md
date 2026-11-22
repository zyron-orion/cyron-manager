# Changelog

All notable changes to CYRON Manager are documented here.

## [Unreleased]

### Added

- Proxy profile manager in Settings: create, edit, delete, and test HTTP, HTTPS, SOCKS5, and SOCKS5H profiles.
- Default proxy, ordered fallbacks, bypass rules, and an optional direct fallback when every proxy fails.
- Per-download proxy choice in the new-download dialog (inherit, direct, or a specific profile).
- Startup warning is now surfaced to the user when the legacy proxy setting cannot be migrated.
- CI now runs the browser extension test suite and regenerates Wails bindings during build.

### Changed

- The single free-text proxy setting was replaced by proxy profiles. A legacy value still migrates automatically on first save.
- Settings gain `proxyDefaultId`, `proxyBypass`, `proxyTestUrl`, `proxyFallbackIds`, and `proxyAllowDirectFallback`.

### Fixed

- The proxy box in Settings silently discarded whatever the user typed. Values are now saved as real profiles.
- The sidebar settings button did nothing; it now opens Settings like the header button.
- Bypass lists were stored with newline separators, which a single-line input strips, merging entries into one broken host. They now round-trip as comma-separated values.
- The settings tooltip stayed in Persian after switching to English; `data-i18n-title` is now translated too.
- An unsaved default-proxy choice in the Settings dialog leaked into app state and survived Cancel.

### Removed

- Dead "toast" runtime event listener with no backend emitter.
- Empty `internal/clipboard` package directory.
- Unused translation keys and ~2,100 lines of dead or duplicated CSS.
- Stale `CYRON-Setup-1.0.0.exe` and `build/windows/icon.old.ico` artifacts.

## [1.1.0] - 2026-11-20

Browser connection release.

### Added

- Fixed-ID Chrome and Edge extension with a local origin-only bridge. No pairing token.
- Capture modes: automatic, ask before adding, and extension-button only.
- Category filters, site exclusions, context menu actions, keyboard shortcut, and recent download states.
- Optional cookie forwarding for signed-in sites. Forwarded cookies use DPAPI and are cleared when a download completes.
- Extension connection test, cookie permission controls, and in-app extension folder access.
- Browser extension ZIP in build output and GitHub releases.
- Browser integration guide in `docs/browser-integration.md`.

### Changed

- The old browser bridge token was removed. The bridge now accepts protected requests only from the fixed extension origin and validates the local host.
- Browser downloads cannot bypass duplicate detection. The bridge forces `AllowDuplicate` to false.
- The app extracts the bundled extension in place and reports its folder, version, and connection state in Settings.
- The extension requests cookie and web-origin permissions only on demand, not at install time.
- The bridge config and status endpoints use POST with an empty JSON object so Chrome includes the extension origin on every request.

### Fixed

- Browser download metadata now keeps the referrer, user agent, MIME type, and suggested filename.
- Failed extension sends leave the original browser download in place and resume it.
- Pending ask notifications survive service-worker restarts and time out by keeping the browser download.
- Task JSON no longer includes manual usernames, passwords, or cookie headers. Credentials remain available to the download engines but are not serialized to the UI or bridge responses.

## [1.0.0] - 2026-11-19

Initial public release.

### Added

- Direct HTTP/HTTPS downloads with multi-connection support and resume.
- aria2 integration for torrent, magnet, and explicit aria2 downloads.
- yt-dlp and ffmpeg support for video and audio downloads.
- Download queue, pause/resume/cancel, retries, scheduling, and optional shutdown.
- Categories, drag and drop, clipboard detection, and GitHub downloads.
- Speed history, daily and monthly stats, and storage usage.
- Persian and English UI with light and dark themes.
- Chrome/Edge browser bridge extension.
- SQLite storage with DPAPI-protected tokens.
