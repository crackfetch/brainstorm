# Changelog

All notable changes to this project will be documented in this file.

## [0.9.0.0] - 2026-04-14

### Added
- Client Hints stealth: `Sec-CH-UA` and `navigator.userAgentData.brands` now report real Chrome/Chromium brands instead of leaking HeadlessChrome. Sites using Cloudflare, PerimeterX, or DataDome can no longer detect headless mode via Client Hints headers.
- Version-gated `--headless=new`: Chrome <109 automatically falls back to bare `--headless` instead of sending an unrecognized flag that could pop a visible window.
- User-Agent auto-refresh on browser reconnect: if Chrome silently reconnects after a DevTools disconnect, the UA and Client Hints metadata are re-resolved from the live browser.
- Thread-safe Executor: all public methods are now mutex-protected. Safe to call from multiple goroutines concurrently.

### Changed
- `navigator.webdriver` mask now uses `EvalOnNewDocument` instead of `MustEval`. Covers all frames including cross-origin iframes (reCAPTCHA, payment widgets, embeds). Redundant re-injection on page reuse removed.
- `--enable-automation` flag deleted from rod's default launcher flags. Prevents the Chrome automation infobar and navigator.webdriver re-enablement on some Chrome versions.
