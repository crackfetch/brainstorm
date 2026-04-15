# Changelog

All notable changes to this project will be documented in this file.

## [0.9.1.0] - 2026-04-14

### Added
- Inspect combo mode: `--screenshot` and `--eval` flags capture a screenshot and run JS in the same browser session as inspect, saving a cold-start.
- Inspect filters: `--tag input,button` and `--name email,password` filter elements by tag or name attribute. Compose both for precise targeting.
- Compact inspect: `--compact` strips placeholder, href, value, role, and hidden fields from each element for ~40% fewer tokens.
- Multi-action run: `brz run wf.yaml login,export` chains actions in one browser session with fail-fast. Saves N-1 cold-starts.
- Dry-run: `brz run wf.yaml action --dry-run --env KEY=VAL` shows resolved steps without launching Chrome.
- Error context: step failures now include `page_elements` with up to 5 similar selectors from the current page, helping agents avoid a re-inspect round-trip.

### Changed
- Extracted `encodeJSON()` helper to DRY up repeated JSON encoder pattern across all commands.
- Extracted `parseEnvFlags()` with `strings.Cut` to DRY up `--env` parsing between dry-run and normal paths.
- `FilterByTag` is now case-insensitive (matches `--tag INPUT` against lowercase tags from InspectJS).
- `ExtractTagFromSelector` handles CSS combinators (`div > button.submit` correctly extracts `button`).
- `filterElements()` uses a callback pattern to share logic between tag and name filtering.

## [0.9.0.0] - 2026-04-14

### Added
- Client Hints stealth: `Sec-CH-UA` and `navigator.userAgentData.brands` now report real Chrome/Chromium brands instead of leaking HeadlessChrome. Sites using Cloudflare, PerimeterX, or DataDome can no longer detect headless mode via Client Hints headers.
- Version-gated `--headless=new`: Chrome <109 automatically falls back to bare `--headless` instead of sending an unrecognized flag that could pop a visible window.
- User-Agent auto-refresh on browser reconnect: if Chrome silently reconnects after a DevTools disconnect, the UA and Client Hints metadata are re-resolved from the live browser.
- Thread-safe Executor: all public methods are now mutex-protected. Safe to call from multiple goroutines concurrently.

### Changed
- `navigator.webdriver` mask now uses `EvalOnNewDocument` instead of `MustEval`. Covers all frames including cross-origin iframes (reCAPTCHA, payment widgets, embeds). Redundant re-injection on page reuse removed.
- `--enable-automation` flag deleted from rod's default launcher flags. Prevents the Chrome automation infobar and navigator.webdriver re-enablement on some Chrome versions.
