# TODOS

> **Status note (2026-04-14):** All items completed. The stealth items below
> are defense-in-depth for brz users facing anti-bot systems, not blockers
> for hoard. See hoard PR #87 for the full story.

## Stealth

### ~~Delete --enable-automation from rod's default launcher flags~~
**Done (2026-04-14).** `l.Delete("enable-automation")` added to `buildLauncher`.
Launcher test asserts the flag is absent.

### ~~Move navigator.webdriver mask to EvalOnNewDocument~~
**Done (2026-04-14).** `injectStealth` now uses `MustEvalOnNewDocument`,
covering all frames (including cross-origin iframes like reCAPTCHA).
Redundant re-injection on page reuse removed.

### ~~Strip HeadlessChrome from Client Hints (navigator.userAgentData + Sec-CH-UA)~~
**Done (2026-04-14).** `buildUserAgentMetadata` constructs Chrome-branded
`UserAgentMetadata` from the browser's product string, stripping HeadlessChrome.
Applied via `MustSetUserAgent` in both `NavigateTo` and `setupPage`. E2E test
verifies `Sec-CH-UA` header and `navigator.userAgentData.brands` are clean.
Unit tests cover version parsing and brand construction.

### ~~Version-gate --headless=new for Chrome <109~~
**Done (2026-04-14).** `detectChromeVersion` probes system Chrome via `--version`,
caches the result on the Executor. Chrome <109 gets bare `--headless`, 109+ gets
`--headless=new`, unknown defaults to `--headless=new`. Unit tests cover 9 version
parsing cases and 5 launcher flag scenarios.

### ~~Refresh User-Agent on browser reconnect~~
**Done (2026-04-14).** `refreshUserAgentFromBrowser` queries `BrowserGetVersion`
before every `MustSetUserAgent` call. No-op when product string hasn't changed
(fast path: one CDP call + string compare). Updates both legacy UA and Client
Hints metadata on product change.

## Library API

### ~~Mutex-protect Executor for concurrent goroutine use~~
**Done (2026-04-14).** `sync.Mutex` added to Executor. All public methods acquire
the lock; private methods assume the caller holds it. `WaitOnFailure` copies fields
under lock then releases before blocking on stdin. `restart()` uses `closeLocked`/
`startLocked` to avoid double-locking. Race detector test with 40 concurrent
goroutines passes cleanly.
