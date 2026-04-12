# TODOS

> **Status note (2026-04-11):** The hoard headless sync project is DONE.
> The root cause turned out to be Chrome process lifecycle (session cookies
> are in-memory, die when Chrome exits), not stealth detection. The
> stealth items below are still valuable as defense-in-depth for any brz
> user facing anti-bot systems, but they are **NOT blockers for hoard**.
> See hoard PR #87 for the full story. Deprioritize these below any
> hoard-blocking work unless a brz user outside hoard needs them.

## Stealth

### Strip HeadlessChrome from Client Hints (navigator.userAgentData + Sec-CH-UA)
**Priority:** P1

Modern anti-bot systems (Cloudflare, PerimeterX, DataDome) read
`navigator.userAgentData.brands` and the `Sec-CH-UA` / `Sec-CH-UA-Full-Version-List`
request headers, not `navigator.userAgent`. The current UA strip only addresses
the legacy header. In headless mode Chrome still advertises `"HeadlessChrome"`
in `userAgentData.brands` and emits it on every Client Hints HTTP header.

Fix: pass a populated `proto.NetworkSetUserAgentOverride.UserAgentMetadata`
struct to `MustSetUserAgent` with Chrome-branded entries instead of
HeadlessChrome. Add an httptest assertion on `Sec-CH-UA` to lock it in.

### Delete --enable-automation from rod's default launcher flags
**Priority:** P1

Rod's `launcher.New()` defaults include `enable-automation`. This flag adds
the automation infobar in headed mode and may re-enable `navigator.webdriver`
on some Chrome versions even after the JS mask. It is a bigger fingerprint
leak than the UA token.

Fix: `l.Delete("enable-automation")` in `buildLauncher`. Add a launcher-args
unit test asserting the flag is absent.

### Move navigator.webdriver mask to EvalOnNewDocument
**Priority:** P1

`injectStealth` runs against the top-level document only. Cross-origin
iframes (reCAPTCHA, payment widgets, embeds) still see
`navigator.webdriver === true`. Page reuse re-runs the injection which is
why the existing fix had to be made idempotent — using
`page.MustEvalOnNewDocument` runs the script before every document in
every frame and eliminates both bugs at once.

### Version-gate --headless=new for Chrome <109
**Priority:** P2

`--headless=new` shipped in Chrome 109. On Chrome <109 the value is
unrecognized — depending on the build, Chrome falls back to legacy headless,
ignores the flag, or launches headed. The last case would silently pop
visible windows on hoard-agent cron runs.

Fix: probe Chrome version via `launcher.LookPath()` + `--version` once at
startup, only set `--headless=new` on 109+, fall back to bare `--headless`
otherwise.

### Refresh User-Agent on browser reconnect
**Priority:** P3

`resolveUserAgent` runs once in `Start()`. If rod silently reconnects after
a DevTools disconnect, the cached `e.userAgent` may diverge from the
running browser. Low likelihood in current usage but the field is now
load-bearing for stealth.

## Library API

### Mutex-protect Executor for concurrent goroutine use
**Priority:** P3

The `workflow/` package advertises itself as importable. Library consumers
(hoard-agent) call `Start()` and `NavigateTo()` from the same goroutine
today, but the public API does not enforce that. `e.userAgent`, `e.page`,
and `e.LastDownload` have no synchronization.

Fix: add a `sync.Mutex` to `Executor` and document the threading contract.
