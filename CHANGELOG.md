# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added
<<<<<<< HEAD
- `brz run --events=jsonl` streams structured JSONL lifecycle events on stdout (one JSON object per line) so an external orchestrator can react to a workflow as it runs. Stdout becomes pure JSONL — human/log output and the final result line move to stderr. Events: `workflow_start`, `action_start`, `step_start`, `retry_attempt`, `download_started`, `download_completed`, `screenshot_captured`, `step_end` (status `ok` / `error` / `skipped`), `action_end`, `workflow_end`. Required keys per record: `ts` (RFC3339Nano UTC), `seq` (monotonic from 1), `event`. Default behavior with no flag is unchanged. New `internal/events` package with a `JSONL` emitter (lock-protected seq + write so on-wire order matches seq order) and a `Nop` default for zero-overhead callers.
- Selector aliases. New top-level `aliases:` map binds short names to selector strings; reference them from any step as `${aliases.NAME}`. When a site changes one selector used in N places, the user fixes one alias instead of N step lines.
- `aliases_from:` directive loads aliases from external YAML files (each a flat `name: selector` map). Supports `~`, absolute, and workflow-relative paths. Files merge in declaration order; later files override earlier on key collision and emit a warn-level signal. Inline `aliases:` always overrides anything from `aliases_from:`.
- Alias-of-alias chains resolve transitively; cycles are detected at parse time with a clear error (no stack overflow).
- Undefined alias references parse-error with the defined-alias list and a Levenshtein-based "did you mean" suggestion.
- Runtime errors that fail to find an aliased selector now include the alias name (and source file when external) — e.g. `find element ".foo" (alias cart_button from selectors/local.yaml): ...`.
- Path resolution for `aliases_from:` rejects symlinks that escape `$HOME` to prevent a workflow tricking brz into reading arbitrary files via a user-controlled selectors directory.
- `brz fmt <file>...` canonicalizes workflow YAML formatting (think `gofmt`). 2-space indentation, terminal newline, deterministic key order at the workflow / action / step level. Default is in-place; `--diff` prints a unified diff and exits 1 if changes pending (CI mode); `--stdout` writes to stdout; `brz fmt -` reads stdin → stdout. Comments attached to keys/values are preserved through the yaml.v3 Node round-trip. Idempotent: `fmt(fmt(x)) == fmt(x)`. Limitations: blank lines between block-style entries are stripped (yaml.v3 doesn't preserve them) and inner flow mappings (`{a: 1, b: 2}`) lose their cosmetic spaces — both documented in `--help`.
- `brz lint <file>...` is a strict superset of `brz validate --strict`. Schema errors (severity `error`, exit 2) come from the existing strict loader so lint and validate agree. Smell rules: `W101` brittle CSS-in-JS hashed selectors (`.css-XXXX`), `W102` `:nth-child` chains, `W103` deep child combinators (>4), `I201` `sleep` where `wait_*` would do, `W301` `eval` returning JS-truthy used as a condition (the rod strict-bool gotcha), `I401` `download` without explicit timeout, `W501` duplicate step labels in one action, `I601` `${VAR}` not declared in workflow `env:` and not in host env. `--json` emits JSONL with `{file,line,col,severity,code,message}` for editor/CI integration. `--strict` makes warnings fail (exit 1).

### Fixed
- `launchForLogin` now uses `--remote-debugging-port=0` so the OS assigns a free port, eliminating collisions with other Chrome instances that happen to hold port 9222.
- Port is discovered from `<profileDir>/DevToolsActivePort` after Chrome starts (polled up to 5s). New `DebugPort()` getter exposes it to callers.
- `profileDir` is now required when `WithLoginURL` is set (it was always needed in practice; now enforced explicitly with a clear error).

## [0.13.0] - 2026-05-08

### Added
- `handoff` step type pauses a workflow for a human takeover. Switches the browser to headed mode (relaunching from headless and re-navigating to the URL the user was on so the page handle isn't stale), prints a clear stderr banner with the message + current URL + resume condition, then blocks until the resume signal fires. Resume signals: `wait_url` (substring match) or `wait_eval` (JS function expression — wrapped in `Boolean(expr())` so JS-truthy values like `1`, `"ready"`, or DOM nodes resume, not just strict-bool `true`). Default 10m timeout. Use this instead of a long `wait_url` when you want the intent ("stop and let the human drive") to be explicit and the window to be guaranteed visible.
- `step.retry` wraps any step with a retry-on-failure block: `retry: { count, backoff, initial_delay }`. Backoff: `linear`, `exponential`, or `none`. The click-then-download look-ahead survives retries — `pendingDownloadWait` is registered before the retry loop and persists across attempts.
- `action.on_error` names a recovery action that runs after terminal failure (after step retries, after auto-escalation, after eval assertions). Recovery runs once. `ok` stays `false` on the original action; the error string carries `; on_error 'X' recovery succeeded` (or `... also failed: ...`). Depth limit of 1 prevents recovery chains.

### Fixed
- `download` step now honors its `Timeout` field. Previously rod's `WaitDownload` callback could block indefinitely when no download arrived after a click. Default 60s if unset.
- Eval-assertion failures now run `on_error` recovery if configured. Previously the post-action eval-failure path returned without going through the recovery hook.
- Stale `pendingDownloadWait` is cleared when an action fails terminally, so a recovery action's click-then-download flow gets a clean look-ahead instead of inheriting the failed primary's pending state.

### For contributors
- `RunAction` is split into a public lock-then-dispatch entry plus a private `runActionLocked` body, letting recovery actions reuse the existing `e.mu` without trying to re-acquire it.

## [0.12.0] - 2026-05-08

### Added
- `click.visible: true` filters selector matches to visible elements (`offsetParent != null` + non-zero rect) before applying `nth` or `text`. Combined with `nth: -1` (now supported), it cleanly disambiguates the common case where a page-level button and a modal-level submit share the same selector.
- `click.nth: -N` supports negative indices counting from the end of the matched set. `-1` is last, `-2` second-to-last. Positive `nth` keeps its historical 1-indexed-via-zero-elision behavior.
- `download.save_as` is now wired at execution: when set, the captured download is renamed to the target path. `${ENV}` interpolation and leading `~` expansion both supported. Parent dirs auto-created. Falls back to copy+remove if `os.Rename` fails (different filesystem). `download.save_to` is a sibling YAML alias.
- `download.return_to: previous | <url>` re-navigates the tab once the download is captured. `previous` restores the URL the tab was on right before the click that triggered the download — fixes the `about:blank` trap where subsequent `wait_url` / interaction steps fail because the tab navigated away during the download.
- `wait_enabled` step type polls until an element is enabled (no `disabled` attribute, no `aria-disabled="true"`). Pairs with `click` for forms gated by anti-bot challenges that keep the submit button disabled until verification.
- `brz status [--json]` prints a diagnostic snapshot: resolved Chrome profile dir + `SingletonLock` state, running brz-launched Chromium PIDs (PID + exe + user-data-dir), downloads tmpdir occupancy, failure-artifact counts. Always exits 0; informational only.
- `brz logs [--follow] [--limit N] [--json]` lists `*_failed_*.png` and `*_before_*.jpg` artifacts in `$TMPDIR` newest-first. `--follow` polls 1s and emits NDJSON. Use to find the last failure screenshots without `grep`'ing `/tmp` by hand.
- `brz examples [list | cat <name> | scaffold <name> [<dir>]]` surfaces bundled workflow YAML patterns. Examples are embedded in the binary via `//go:embed`. Three new patterns added covering this release's features: `captcha-gated-form`, `click-visible-among-duplicates`, `modal-export-with-save-and-return`.
- `brz validate --strict` switches to `KnownFields(true)` so unknown YAML fields fail validation with a line number and a "Did you mean?" suggestion built from a Levenshtein match against the type's declared yaml tags. The wrapping error preserves `errors.As` / `errors.Is` chains via an `Unwrap()` method.
- Headed-launch announcement: brz prints one stderr line whenever it launches a visible Chromium: `[brz] Headed Chromium launched (pid=N exe=PATH profile=DIR) — Cmd+Tab to focus if not visible`. macOS additionally runs `osascript activate` on the launched bundle so the window comes to foreground instead of opening behind whatever app currently owns focus.

### Changed
- Step-failure error strings now include a one-line "Nearby visible elements" hint when `PageElements` were captured. Hidden elements filtered. Rune-safe truncation for non-ASCII labels. The rich data still lives on `ActionResult.PageElements` for programmatic consumers.

### For contributors
- New package `workflows/examples` embeds the bundled YAML files via `//go:embed` and exposes `Names()`, `Read(name)`, `Summary(yaml)` helpers.
- New test helper `skipIfNoDisplay(t)` skips headed-mode E2E tests on Linux runners without an X / Wayland session.
- The strict-suggest field registry is built once at package init via reflection over every workflow type. Adding a new schema type requires adding it to `buildFieldRegistry`'s slice; tests pin the current set so the registry can't quietly fall behind.

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
